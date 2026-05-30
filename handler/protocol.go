package handler

import (
	"api-in-one/model"
	"api-in-one/relay"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Protocol handles inbound requests in Claude and Gemini formats.
type Protocol struct {
	engine *relay.Engine
}

func NewProtocol(engine *relay.Engine) *Protocol {
	return &Protocol{engine: engine}
}

// ==================== Claude Inbound ====================

// ClaudeMessages handles POST /v1/messages (Claude format inbound)
func (h *Protocol) ClaudeMessages(c *gin.Context) {
	var claudeReq claudeInboundRequest
	if err := c.ShouldBindJSON(&claudeReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	// Convert Claude → OpenAI
	oaiReq := claudeToOpenAI(&claudeReq)

	start := time.Now()
	result, err := h.engine.Do(c.Request.Context(), oaiReq, "claude")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
			"type":    "upstream_error",
		}})
		logRequest("claude-inbound", claudeReq.Model, 0, time.Since(start), err)
		return
	}

	logRequest("claude-inbound", claudeReq.Model, 200, time.Since(start), nil)

	// Clean up fake tool calls from response text (model may generate
	// tool call syntax like <function=Bash>... when it doesn't support tools)
	if result.Response != nil {
		cleanResponseToolCalls(result.Response)
	}

	if claudeReq.Stream {
		h.handleClaudeStream(c, result)
		return
	}

	// Convert OpenAI response → Claude response
	claudeResp := openAIToClaude(result.Response)
	c.JSON(http.StatusOK, claudeResp)
}

func (h *Protocol) handleClaudeStream(c *gin.Context, result *relay.RelayResult) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	writeClaudeEvent := func(event string, data interface{}) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		flusher.Flush()
	}

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())
	blockIndex := 0
	textStarted := false
	textAccum := ""
	reasoningAccum := ""
	toolCallStates := map[int]*struct {
		blockIndex int
		callID     string
		name       string
		argsBuf    string
		started    bool
	}{}

	// message_start
	writeClaudeEvent("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []interface{}{}, "model": "unknown",
			"stop_reason": nil, "usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})

	startTextBlock := func() {
		if textStarted {
			return
		}
		textStarted = true
		writeClaudeEvent("content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": blockIndex,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		})
	}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		chunk := parseOpenAIChunk(data)
		if chunk == nil || len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Reasoning content
		if delta.ReasoningContent != "" {
			reasoningAccum += delta.ReasoningContent
		}

		// Text content
		if delta.Content != nil {
			if text, ok := delta.Content.(string); ok && text != "" {
				startTextBlock()
				textAccum += text
				writeClaudeEvent("content_block_delta", map[string]interface{}{
					"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": text},
				})
			}
		}

		// Tool calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				st, exists := toolCallStates[idx]
				if !exists {
					// Close text block if open
					if textStarted {
						writeClaudeEvent("content_block_stop", map[string]interface{}{
							"type": "content_block_stop", "index": blockIndex,
						})
						blockIndex++
						textStarted = false
					}
					st = &struct {
						blockIndex int
						callID     string
						name       string
						argsBuf    string
						started    bool
					}{
						blockIndex: blockIndex,
						callID:     tc.ID,
						name:       tc.Function.Name,
					}
					if st.callID == "" {
						st.callID = fmt.Sprintf("call_%d", time.Now().UnixMilli())
					}
					toolCallStates[idx] = st
					blockIndex++
				}
				if tc.Function.Name != "" && st.name == "" {
					st.name = tc.Function.Name
				}
				if !st.started && st.name != "" {
					st.started = true
					writeClaudeEvent("content_block_start", map[string]interface{}{
						"type": "content_block_start", "index": st.blockIndex,
						"content_block": map[string]interface{}{
							"type": "tool_use", "id": st.callID, "name": st.name,
						},
					})
				}
				if tc.Function.Arguments != "" {
					st.argsBuf += tc.Function.Arguments
					if st.started {
						writeClaudeEvent("content_block_delta", map[string]interface{}{
							"type": "content_block_delta", "index": st.blockIndex,
							"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
						})
					}
				}
			}
		}

		if chunk.Choices[0].FinishReason != nil {
			break
		}
	}

	// Finalize text block
	if textStarted {
		writeClaudeEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": blockIndex,
		})
		blockIndex++
	}

	// Finalize tool call blocks
	for _, st := range toolCallStates {
		if st.name == "" {
			st.name = "tool"
		}
		if !st.started {
			writeClaudeEvent("content_block_start", map[string]interface{}{
				"type": "content_block_start", "index": st.blockIndex,
				"content_block": map[string]interface{}{
					"type": "tool_use", "id": st.callID, "name": st.name,
				},
			})
			if st.argsBuf != "" {
				writeClaudeEvent("content_block_delta", map[string]interface{}{
					"type": "content_block_delta", "index": st.blockIndex,
					"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": st.argsBuf},
				})
			}
		}
		writeClaudeEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": st.blockIndex,
		})
	}

	// message_delta with stop_reason
	stopReason := "end_turn"
	if len(toolCallStates) > 0 {
		stopReason = "tool_use"
	}
	writeClaudeEvent("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason},
		"usage": map[string]interface{}{"output_tokens": 0},
	})

	// message_stop
	writeClaudeEvent("message_stop", map[string]interface{}{"type": "message_stop"})
}

// ==================== Gemini Inbound ====================

// GeminiGenerate handles POST /v1beta/models/:model:generateContent (Gemini format inbound)
func (h *Protocol) GeminiGenerate(c *gin.Context) {
	modelName := c.Param("model")
	// Strip ":generateContent" or ":streamGenerateContent" suffix
	modelName = strings.TrimSuffix(modelName, ":generateContent")
	modelName = strings.TrimSuffix(modelName, ":streamGenerateContent")

	var geminiReq geminiInboundRequest
	if err := c.ShouldBindJSON(&geminiReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	oaiReq := geminiToOpenAI(&geminiReq, modelName)

	start := time.Now()
	result, err := h.engine.Do(c.Request.Context(), oaiReq, "gemini")
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
		}})
		logRequest("gemini-inbound", modelName, 0, time.Since(start), err)
		return
	}

	logRequest("gemini-inbound", modelName, 200, time.Since(start), nil)

	isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")
	if isStream {
		h.handleGeminiStream(c, result)
		return
	}

	geminiResp := openAIToGemini(result.Response)
	c.JSON(http.StatusOK, geminiResp)
}

func (h *Protocol) handleGeminiStream(c *gin.Context, result *relay.RelayResult) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}

		chunk := parseOpenAIChunk(data)
		if chunk == nil {
			continue
		}

		text := ""
		var finishReason *string
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			if chunk.Choices[0].Delta.Content != nil {
				text, _ = chunk.Choices[0].Delta.Content.(string)
			}
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
			fr := mapFinishReasonToGemini(*chunk.Choices[0].FinishReason)
			finishReason = &fr
		}

		resp := map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content": map[string]interface{}{
					"parts": []map[string]interface{}{{"text": text}},
					"role":  "model",
				},
				"finishReason": finishReason,
			}},
		}

		b, _ := json.Marshal(resp)
		c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
		flusher.Flush()

		if finishReason != nil {
			return
		}
	}
}

// ==================== Conversion Functions ====================

// Claude → OpenAI

type claudeInboundRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    interface{} `json:"system,omitempty"` // string or []content block
	Messages  []struct {
		Role    string      `json:"role"`
		Content interface{} `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream,omitempty"`
	Tools  []struct {
		Name        string      `json:"name"`
		Description string      `json:"description,omitempty"`
		InputSchema interface{} `json:"input_schema"`
	} `json:"tools,omitempty"`
}

func extractSystemText(system interface{}) string {
	if system == nil {
		return ""
	}
	switch v := system.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, p := range v {
			if m, ok := p.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "\n")
	default:
		return fmt.Sprintf("%v", v)
	}
}

func claudeToOpenAI(req *claudeInboundRequest) *model.ChatCompletionRequest {
	oaiReq := &model.ChatCompletionRequest{
		Model:  req.Model,
		Stream: req.Stream,
	}
	if req.MaxTokens > 0 {
		oaiReq.MaxTokens = &req.MaxTokens
	}

	// System message
	systemText := extractSystemText(req.System)
	if systemText != "" {
		oaiReq.Messages = append(oaiReq.Messages, model.Message{
			Role:    "system",
			Content: systemText,
		})
	}

	// Convert tools: Claude format → OpenAI format
	for _, tool := range req.Tools {
		oaiTool := model.Tool{
			Type: "function",
			Function: model.ToolFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  tool.InputSchema,
			},
		}
		oaiReq.Tools = append(oaiReq.Tools, oaiTool)
	}

	// Convert messages
	for _, msg := range req.Messages {
		content := msg.Content

		// Check if content is an array of content blocks
		if blocks, ok := content.([]interface{}); ok {
			hasToolUse := false
			hasToolResult := false

			// First pass: detect block types
			for _, b := range blocks {
				if m, ok := b.(map[string]interface{}); ok {
					switch m["type"] {
					case "tool_use":
						hasToolUse = true
					case "tool_result":
						hasToolResult = true
					}
				}
			}

			if hasToolResult {
				// User message with tool_result → convert to tool role messages
				for _, b := range blocks {
					if m, ok := b.(map[string]interface{}); ok && m["type"] == "tool_result" {
						toolMsg := model.Message{
							Role:       "tool",
							ToolCallID: getString(m, "tool_use_id"),
						}
						switch rc := m["content"].(type) {
						case string:
							toolMsg.Content = rc
						case []interface{}:
							var parts []string
							for _, p := range rc {
								if pm, ok := p.(map[string]interface{}); ok && pm["type"] == "text" {
									parts = append(parts, getString(pm, "text"))
								}
							}
							toolMsg.Content = strings.Join(parts, "\n")
						default:
							toolMsg.Content = fmt.Sprintf("%v", rc)
						}
						oaiReq.Messages = append(oaiReq.Messages, toolMsg)
					}
				}
				continue
			}

			if hasToolUse {
				// Assistant message with tool_use → convert to OpenAI tool_calls format
				oaiMsg := model.Message{Role: "assistant"}
				var textParts []string
				var toolCalls []model.ToolCall

				for _, b := range blocks {
					if m, ok := b.(map[string]interface{}); ok {
						switch m["type"] {
						case "text":
							textParts = append(textParts, getString(m, "text"))
						case "tool_use":
							inputJSON, _ := json.Marshal(m["input"])
							toolCalls = append(toolCalls, model.ToolCall{
								ID:   getString(m, "id"),
								Type: "function",
								Function: model.FunctionCall{
									Name:      getString(m, "name"),
									Arguments: string(inputJSON),
								},
							})
						}
					}
				}

				if len(textParts) > 0 {
					oaiMsg.Content = strings.Join(textParts, "\n")
				}
				if len(toolCalls) > 0 {
					oaiMsg.ToolCalls = toolCalls
				}
				oaiReq.Messages = append(oaiReq.Messages, oaiMsg)
				continue
			}

			// Regular text content blocks
			var parts []model.ContentPart
			for _, b := range blocks {
				if m, ok := b.(map[string]interface{}); ok && m["type"] == "text" {
					parts = append(parts, model.ContentPart{Type: "text", Text: getString(m, "text")})
				}
			}
			oaiReq.Messages = append(oaiReq.Messages, model.Message{
				Role:    msg.Role,
				Content: parts,
			})
			continue
		}

		// Simple string content
		oaiReq.Messages = append(oaiReq.Messages, model.Message{
			Role:    msg.Role,
			Content: content,
		})
	}
	return oaiReq
}

func openAIToClaude(resp *model.ChatCompletionResponse) map[string]interface{} {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return map[string]interface{}{
			"id": resp.ID, "type": "message", "role": "assistant",
			"content": []interface{}{}, "stop_reason": "end_turn",
		}
	}

	msg := resp.Choices[0].Message
	var contentBlocks []interface{}

	// Thinking/reasoning content
	if msg.ReasoningContent != "" {
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type": "thinking", "thinking": msg.ReasoningContent,
		})
	}

	// Text content
	if msg.Content != nil {
		text := ""
		if s, ok := msg.Content.(string); ok {
			text = s
		}
		if text != "" {
			contentBlocks = append(contentBlocks, map[string]interface{}{
				"type": "text", "text": text,
			})
		}
	}

	// Tool calls → tool_use content blocks
	for _, tc := range msg.ToolCalls {
		inputMap := map[string]interface{}{}
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &inputMap)
		}
		contentBlocks = append(contentBlocks, map[string]interface{}{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Function.Name,
			"input": inputMap,
		})
	}

	stopReason := "end_turn"
	if resp.Choices[0].FinishReason != nil {
		stopReason = mapFinishReasonToClaude(*resp.Choices[0].FinishReason)
	}

	return map[string]interface{}{
		"id":          resp.ID,
		"type":        "message",
		"role":        "assistant",
		"content":     contentBlocks,
		"model":       resp.Model,
		"stop_reason": stopReason,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
		},
	}
}

func getString(m map[string]interface{}, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// Gemini → OpenAI

type geminiInboundRequest struct {
	Contents []struct {
		Role  string `json:"role"`
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"contents"`
	SystemInstruction *struct {
		Parts []struct {
			Text string `json:"text"`
		} `json:"parts"`
	} `json:"systemInstruction,omitempty"`
	GenerationConfig *struct {
		MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
		Temperature     float64 `json:"temperature,omitempty"`
		TopP            float64 `json:"topP,omitempty"`
	} `json:"generationConfig,omitempty"`
}

func geminiToOpenAI(req *geminiInboundRequest, modelName string) *model.ChatCompletionRequest {
	oaiReq := &model.ChatCompletionRequest{
		Model: modelName,
	}

	if req.SystemInstruction != nil {
		text := ""
		for _, p := range req.SystemInstruction.Parts {
			text += p.Text
		}
		oaiReq.Messages = append(oaiReq.Messages, model.Message{Role: "system", Content: text})
	}

	for _, content := range req.Contents {
		role := content.Role
		if role == "model" {
			role = "assistant"
		}
		text := ""
		for _, p := range content.Parts {
			text += p.Text
		}
		oaiReq.Messages = append(oaiReq.Messages, model.Message{Role: role, Content: text})
	}

	if req.GenerationConfig != nil {
		if req.GenerationConfig.MaxOutputTokens > 0 {
			oaiReq.MaxTokens = &req.GenerationConfig.MaxOutputTokens
		}
		if req.GenerationConfig.Temperature > 0 {
			oaiReq.Temperature = &req.GenerationConfig.Temperature
		}
		if req.GenerationConfig.TopP > 0 {
			oaiReq.TopP = &req.GenerationConfig.TopP
		}
	}

	return oaiReq
}

func openAIToGemini(resp *model.ChatCompletionResponse) map[string]interface{} {
	text := ""
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		if s, ok := resp.Choices[0].Message.Content.(string); ok {
			text = s
		}
	}
	stopReason := "STOP"
	if len(resp.Choices) > 0 && resp.Choices[0].FinishReason != nil {
		stopReason = mapFinishReasonToGemini(*resp.Choices[0].FinishReason)
	}
	return map[string]interface{}{
		"candidates": []map[string]interface{}{{
			"content": map[string]interface{}{
				"parts": []map[string]interface{}{{"text": text}},
				"role":  "model",
			},
			"finishReason": stopReason,
		}},
		"usageMetadata": map[string]interface{}{
			"promptTokenCount":     resp.Usage.PromptTokens,
			"candidatesTokenCount": resp.Usage.CompletionTokens,
			"totalTokenCount":      resp.Usage.TotalTokens,
		},
	}
}

// Helpers

func mapFinishReasonToClaude(reason string) string {
	switch reason {
	case "stop":
		return "end_turn"
	case "length":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func mapFinishReasonToGemini(reason string) string {
	switch reason {
	case "stop":
		return "STOP"
	case "length":
		return "MAX_TOKENS"
	case "content_filter":
		return "SAFETY"
	default:
		return "STOP"
	}
}

func parseOpenAIChunk(data []byte) *model.ChatCompletionChunk {
	// Strip "data: " prefix if present
	s := string(data)
	if strings.HasPrefix(s, "data: ") {
		s = strings.TrimPrefix(s, "data: ")
		s = strings.TrimSpace(s)
		if s == "[DONE]" {
			return nil
		}
	}
	var chunk model.ChatCompletionChunk
	if err := json.Unmarshal([]byte(s), &chunk); err != nil {
		return nil
	}
	return &chunk
}

// ==================== OpenAI Responses API ====================

// Responses handles POST /v1/responses (OpenAI Responses API format)
func (h *Protocol) Responses(c *gin.Context) {
	var req responsesInboundRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	oaiReq := responsesToChatCompletion(&req)

	start := time.Now()
	result, err := h.engine.Do(c.Request.Context(), oaiReq, "responses")
	if err != nil {
		logRequest("responses", req.Model, 502, time.Since(start), err)
		c.JSON(http.StatusBadGateway, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
			"type":    "upstream_error",
		}})
		return
	}

	logRequest("responses", req.Model, 200, time.Since(start), nil)

	if req.Stream {
		h.handleResponsesStream(c, result, req.Model)
		return
	}

	resp := chatCompletionToResponses(result.Response, req.Model)
	c.JSON(http.StatusOK, resp)
}

func (h *Protocol) handleResponsesStream(c *gin.Context, result *relay.RelayResult, modelName string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	respID := fmt.Sprintf("resp_%d", time.Now().UnixMilli())
	outputIndex := 0
	reasonID := fmt.Sprintf("reason_%d", time.Now().UnixMilli())
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())

	writeEvent := func(event string, data map[string]interface{}) {
		data["type"] = event
		b, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		flusher.Flush()
	}

	writeEvent("response.created", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response", "status": "in_progress", "model": modelName, "output": []interface{}{},
		},
	})

	reasoningAccum := ""
	textAccum := ""
	reasoningStarted := false
	messageStarted := false

	startReasoning := func() {
		if reasoningStarted {
			return
		}
		reasoningStarted = true
		writeEvent("response.output_item.added", map[string]interface{}{
			"output_index": outputIndex,
			"item": map[string]interface{}{
				"id": reasonID, "type": "reasoning", "summary": []interface{}{}, "status": "in_progress",
			},
		})
		writeEvent("response.reasoning_summary_part.added", map[string]interface{}{
			"item_id": reasonID, "output_index": outputIndex, "summary_index": 0,
			"part": map[string]interface{}{"type": "summary_text", "text": ""},
		})
	}

	startMessage := func() {
		if messageStarted {
			return
		}
		if reasoningStarted {
			// Finalize reasoning first
			writeEvent("response.reasoning_summary_text.done", map[string]interface{}{
				"item_id": reasonID, "output_index": outputIndex, "summary_index": 0, "text": reasoningAccum,
			})
			writeEvent("response.reasoning_summary_part.done", map[string]interface{}{
				"item_id": reasonID, "output_index": outputIndex, "summary_index": 0,
				"part": map[string]interface{}{"type": "summary_text", "text": reasoningAccum},
			})
			writeEvent("response.output_item.done", map[string]interface{}{
				"output_index": outputIndex,
				"item": map[string]interface{}{
					"id": reasonID, "type": "reasoning",
					"summary":           []map[string]interface{}{{"type": "summary_text", "text": reasoningAccum}},
					"encrypted_content": reasoningAccum, "status": "completed",
				},
			})
			outputIndex++
		}
		messageStarted = true
		writeEvent("response.output_item.added", map[string]interface{}{
			"output_index": outputIndex,
			"item": map[string]interface{}{
				"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []interface{}{},
			},
		})
		writeEvent("response.content_part.added", map[string]interface{}{
			"output_index": outputIndex, "content_index": 0,
			"part": map[string]interface{}{"type": "output_text", "text": ""},
		})
	}

	output := []map[string]interface{}{}

	// Track tool_calls state
	type toolCallState struct {
		itemID   string
		outIndex int
		callID   string
		name     string
		argsBuf  string
	}
	toolCalls := map[int]*toolCallState{}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		chunk := parseOpenAIChunk(data)
		if chunk == nil || len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Reasoning content
		if delta.ReasoningContent != "" {
			startReasoning()
			reasoningAccum += delta.ReasoningContent
			writeEvent("response.reasoning_summary_text.delta", map[string]interface{}{
				"item_id": reasonID, "output_index": outputIndex, "summary_index": 0, "delta": delta.ReasoningContent,
			})
		}

		// Text content
		text := ""
		if delta.Content != nil {
			text, _ = delta.Content.(string)
		}
		if text != "" {
			startMessage()
			textAccum += text
			writeEvent("response.output_text.delta", map[string]interface{}{
				"output_index": outputIndex, "content_index": 0, "delta": text,
			})
		}

		// Tool calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				st, exists := toolCalls[idx]
				if !exists {
					// New tool call
					st = &toolCallState{
						itemID:   fmt.Sprintf("fc_%d_%d", time.Now().UnixMilli(), idx),
						outIndex: outputIndex,
						callID:   tc.ID,
						name:     tc.Function.Name,
					}
					if st.callID == "" {
						st.callID = fmt.Sprintf("call_%s", st.itemID[3:])
					}
					toolCalls[idx] = st
					outputIndex++

					writeEvent("response.output_item.added", map[string]interface{}{
						"output_index": st.outIndex,
						"item": map[string]interface{}{
							"id": st.itemID, "type": "function_call", "call_id": st.callID,
							"name": st.name, "arguments": "", "status": "in_progress",
						},
					})
				}
				if tc.Function.Name != "" && st.name == "" {
					st.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					st.argsBuf += tc.Function.Arguments
					writeEvent("response.function_call_arguments.delta", map[string]interface{}{
						"item_id": st.itemID, "output_index": st.outIndex, "delta": tc.Function.Arguments,
					})
				}
			}
		}

		if chunk.Choices[0].FinishReason != nil {
			break
		}
	}

	// Finalize tool calls
	for _, st := range toolCalls {
		safeArgs := st.argsBuf
		if safeArgs == "" {
			safeArgs = "{}"
		} else if !json.Valid([]byte(safeArgs)) {
			safeArgs = "{}"
		}
		writeEvent("response.function_call_arguments.done", map[string]interface{}{
			"item_id": st.itemID, "output_index": st.outIndex, "arguments": safeArgs,
		})
		output = append(output, map[string]interface{}{
			"id": st.itemID, "type": "function_call", "call_id": st.callID,
			"name": st.name, "arguments": safeArgs, "status": "completed",
		})
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": st.outIndex, "item": map[string]interface{}{
				"id": st.itemID, "type": "function_call", "call_id": st.callID,
				"name": st.name, "arguments": safeArgs, "status": "completed",
			},
		})
	}

	// Finalize text message (only if there was text)
	if textAccum != "" || len(toolCalls) == 0 {
		if !messageStarted {
			startMessage()
		}
		writeEvent("response.output_text.done", map[string]interface{}{
			"output_index": outputIndex, "content_index": 0, "text": textAccum,
		})
		writeEvent("response.content_part.done", map[string]interface{}{
			"output_index": outputIndex, "content_index": 0,
			"part": map[string]interface{}{"type": "output_text", "text": textAccum},
		})
		msgItem := map[string]interface{}{
			"id": msgID, "type": "message", "role": "assistant", "status": "completed",
			"content": []map[string]interface{}{{"type": "output_text", "text": textAccum}},
		}
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": outputIndex, "item": msgItem,
		})
		output = append(output, msgItem)
	}

	// response.completed
	writeEvent("response.completed", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response", "status": "completed", "model": modelName, "output": output,
		},
	})
}

// ---- Responses API structures ----

type responsesInboundRequest struct {
	Model             string        `json:"model"`
	Input             interface{}   `json:"input"`        // string or []responsesInputItem
	Instructions      string        `json:"instructions"` // system prompt
	Stream            bool          `json:"stream"`
	MaxOutputTokens   int           `json:"max_output_tokens"`
	Temperature       *float64      `json:"temperature"`
	TopP              *float64      `json:"top_p"`
	Tools             []interface{} `json:"tools,omitempty"`
	ToolChoice        interface{}   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool         `json:"parallel_tool_calls,omitempty"`
}

type responsesInputItem struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"`
}

func responsesToChatCompletion(req *responsesInboundRequest) *model.ChatCompletionRequest {
	oaiReq := &model.ChatCompletionRequest{
		Model:  req.Model,
		Stream: req.Stream,
	}
	if req.MaxOutputTokens > 0 {
		oaiReq.MaxTokens = &req.MaxOutputTokens
	}
	if req.Temperature != nil {
		oaiReq.Temperature = req.Temperature
	}
	if req.TopP != nil {
		oaiReq.TopP = req.TopP
	}

	if req.Instructions != "" {
		oaiReq.Messages = append(oaiReq.Messages, model.Message{
			Role:    "system",
			Content: req.Instructions,
		})
	}

	// Convert tools: Responses API format → Chat Completions format
	for _, t := range req.Tools {
		tm, ok := t.(map[string]interface{})
		if !ok {
			continue
		}
		toolType, _ := tm["type"].(string)
		if toolType == "function" {
			oaiTool := model.Tool{Type: "function"}
			if name, ok := tm["name"].(string); ok {
				oaiTool.Function.Name = name
			}
			if desc, ok := tm["description"].(string); ok {
				oaiTool.Function.Description = desc
			}
			if params, ok := tm["parameters"]; ok {
				oaiTool.Function.Parameters = params
			}
			oaiReq.Tools = append(oaiReq.Tools, oaiTool)
		}
		// Skip non-function tools (web_search, code_interpreter, etc.)
	}

	// Convert tool_choice
	if req.ToolChoice != nil {
		switch tc := req.ToolChoice.(type) {
		case string:
			oaiReq.ToolChoice = tc
		case map[string]interface{}:
			if fn, ok := tc["function"].(map[string]interface{}); ok {
				if name, ok := fn["name"].(string); ok {
					oaiReq.ToolChoice = map[string]interface{}{
						"type":     "function",
						"function": map[string]interface{}{"name": name},
					}
				}
			}
		}
	}

	// Input items → messages
	// Uses buffering to assemble reasoning + function_calls into one assistant message
	switch v := req.Input.(type) {
	case string:
		oaiReq.Messages = append(oaiReq.Messages, model.Message{
			Role:    "user",
			Content: v,
		})
	case []interface{}:
		// Buffer for assembling assistant turn (reasoning + tool_calls + text)
		var pendingReasoning string
		var pendingToolCalls []model.ToolCall
		var pendingText string

		flushPending := func() {
			hasReasoning := pendingReasoning != ""
			hasTools := len(pendingToolCalls) > 0
			hasText := pendingText != ""
			if !hasReasoning && !hasTools && !hasText {
				return
			}
			msg := model.Message{Role: "assistant"}
			if hasText {
				msg.Content = pendingText
			} else if !hasTools {
				msg.Content = ""
			}
			if hasTools {
				msg.ToolCalls = pendingToolCalls
			}
			if hasReasoning {
				msg.ReasoningContent = pendingReasoning
			}
			oaiReq.Messages = append(oaiReq.Messages, msg)
			pendingReasoning = ""
			pendingToolCalls = nil
			pendingText = ""
		}

		for _, item := range v {
			m, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			itemType, _ := m["type"].(string)

			switch itemType {
			case "reasoning":
				// Buffer reasoning content (MiMo requires it back)
				encrypted, _ := m["encrypted_content"].(string)
				if encrypted == "" {
					// Try summary text as fallback
					if summary, ok := m["summary"].([]interface{}); ok {
						for _, s := range summary {
							if sm, ok := s.(map[string]interface{}); ok {
								if sm["type"] == "summary_text" {
									encrypted += getString(sm, "text")
								}
							}
						}
					}
				}
				if encrypted != "" {
					pendingReasoning += encrypted
				}

			case "function_call":
				// Buffer tool call (may be multiple in same turn)
				callID := getString(m, "call_id")
				name := getString(m, "name")
				args := getString(m, "arguments")
				if args == "" {
					args = "{}"
				}
				pendingToolCalls = append(pendingToolCalls, model.ToolCall{
					ID:   callID,
					Type: "function",
					Function: model.FunctionCall{
						Name:      name,
						Arguments: args,
					},
				})

			case "message":
				role, _ := m["role"].(string)
				if role == "assistant" {
					// Buffer assistant text
					content := extractContentText(m["content"])
					if s, ok := content.(string); ok {
						pendingText = s
					}
				} else {
					// Non-assistant message → flush pending and add
					flushPending()
					msg := model.Message{Role: role}
					msg.Content = extractContentText(m["content"])
					oaiReq.Messages = append(oaiReq.Messages, msg)
				}

			case "function_call_output":
				// Tool result → flush pending assistant turn, then add tool message
				flushPending()
				oaiReq.Messages = append(oaiReq.Messages, model.Message{
					Role:       "tool",
					ToolCallID: getString(m, "call_id"),
					Content:    getString(m, "output"),
				})

			default:
				// Unknown item type → try as message
				flushPending()
				role, _ := m["role"].(string)
				if role != "" {
					msg := model.Message{Role: role}
					msg.Content = extractContentText(m["content"])
					oaiReq.Messages = append(oaiReq.Messages, msg)
				}
			}
		}
		// Flush any remaining buffered assistant turn
		flushPending()
	}

	// Post-process: merge consecutive same-role messages
	oaiReq.Messages = mergeConsecutiveRoles(oaiReq.Messages)

	return oaiReq
}

// mergeConsecutiveRoles merges consecutive messages with the same role.
// MiMo requires alternating user/assistant roles.
func mergeConsecutiveRoles(msgs []model.Message) []model.Message {
	if len(msgs) <= 1 {
		return msgs
	}
	var merged []model.Message
	for _, msg := range msgs {
		if len(merged) > 0 && merged[len(merged)-1].Role == msg.Role && msg.Role != "system" {
			prev := &merged[len(merged)-1]
			// Merge content
			prevText := contentToString(prev.Content)
			curText := contentToString(msg.Content)
			if prevText != "" && curText != "" {
				prev.Content = prevText + "\n" + curText
			} else if curText != "" {
				prev.Content = curText
			}
			// Merge tool_calls
			prev.ToolCalls = append(prev.ToolCalls, msg.ToolCalls...)
			// Merge reasoning
			if msg.ReasoningContent != "" {
				prev.ReasoningContent += "\n" + msg.ReasoningContent
			}
		} else {
			merged = append(merged, msg)
		}
	}
	return merged
}

func extractContentText(content interface{}) interface{} {
	if content == nil {
		return ""
	}
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []model.ContentPart
		for _, p := range v {
			if m, ok := p.(map[string]interface{}); ok {
				partType, _ := m["type"].(string)
				if partType == "input_text" || partType == "text" {
					text, _ := m["text"].(string)
					parts = append(parts, model.ContentPart{Type: "text", Text: text})
				}
			}
		}
		if len(parts) > 0 {
			return parts
		}
		return ""
	default:
		return fmt.Sprintf("%v", v)
	}
}

func chatCompletionToResponses(resp *model.ChatCompletionResponse, modelName string) map[string]interface{} {
	var output []map[string]interface{}

	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		msg := resp.Choices[0].Message

		// Reasoning → reasoning output item with encrypted_content
		// MiMo requires reasoning_content to be passed back in multi-turn.
		// Codex treats encrypted_content as opaque and echoes it back.
		if msg.ReasoningContent != "" {
			output = append(output, map[string]interface{}{
				"type":              "reasoning",
				"id":                fmt.Sprintf("reason_%s", resp.ID),
				"summary":           []map[string]interface{}{{"type": "summary_text", "text": msg.ReasoningContent}},
				"encrypted_content": msg.ReasoningContent,
				"status":            "completed",
			})
		}

		// Text content
		text := ""
		if s, ok := msg.Content.(string); ok {
			text = s
		}
		output = append(output, map[string]interface{}{
			"type":    "message",
			"id":      fmt.Sprintf("msg_%s", resp.ID),
			"role":    "assistant",
			"status":  "completed",
			"content": []map[string]interface{}{{"type": "output_text", "text": text, "annotations": []interface{}{}}},
		})

		// Tool calls → function_call output items
		for _, tc := range msg.ToolCalls {
			output = append(output, map[string]interface{}{
				"type":      "function_call",
				"id":        fmt.Sprintf("fc_%s", tc.ID),
				"call_id":   tc.ID,
				"name":      tc.Function.Name,
				"arguments": tc.Function.Arguments,
				"status":    "completed",
			})
		}
	}

	return map[string]interface{}{
		"id":         resp.ID,
		"object":     "response",
		"created_at": resp.Created,
		"status":     "completed",
		"model":      modelName,
		"output":     output,
		"usage": map[string]interface{}{
			"input_tokens":  resp.Usage.PromptTokens,
			"output_tokens": resp.Usage.CompletionTokens,
			"total_tokens":  resp.Usage.TotalTokens,
		},
	}
}

// cleanResponseToolCalls removes fake tool call syntax from model response text.
func cleanResponseToolCalls(resp *model.ChatCompletionResponse) {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return
	}
	msg := resp.Choices[0].Message
	if msg.Content == nil {
		return
	}
	text, ok := msg.Content.(string)
	if !ok || text == "" {
		return
	}
	cleaned := removeFakeToolCalls(text)
	if cleaned != text {
		msg.Content = strings.TrimSpace(cleaned)
	}
}

func removeFakeToolCalls(text string) string {
	prefixes := []string{"<function="}
	closings := []string{"</function"}
	for i, prefix := range prefixes {
		closing := closings[i]
		for {
			idx := strings.Index(text, prefix)
			if idx == -1 {
				break
			}
			rest := text[idx:]
			endIdx := strings.Index(rest, closing)
			if endIdx != -1 {
				end := endIdx + len(closing)
				if end < len(rest) && rest[end] == '>' {
					end++
				}
				text = strings.TrimSpace(text[:idx] + rest[end:])
			} else {
				text = strings.TrimSpace(text[:idx])
				break
			}
		}
	}
	return text
}

func contentToString(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	if parts, ok := content.([]model.ContentPart); ok {
		var texts []string
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "")
	}
	return fmt.Sprintf("%v", content)
}
