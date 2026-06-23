package handler

import (
	"api-in-one/model"
	"encoding/json"
	"fmt"
	"strings"
)

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
				// User message with tool_result → preserve surrounding text and tool role messages.
				var textParts []string
				for _, b := range blocks {
					if m, ok := b.(map[string]interface{}); ok {
						if m["type"] == "text" {
							textParts = append(textParts, getString(m, "text"))
						}
					}
				}
				if text := strings.TrimSpace(strings.Join(textParts, "\n")); text != "" {
					oaiReq.Messages = append(oaiReq.Messages, model.Message{
						Role:    msg.Role,
						Content: text,
					})
				}
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
						case "thinking":
							oaiMsg.ReasoningContent += getString(m, "thinking")
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
			reasoning := ""
			for _, b := range blocks {
				if m, ok := b.(map[string]interface{}); ok {
					switch m["type"] {
					case "text":
						parts = append(parts, model.ContentPart{Type: "text", Text: getString(m, "text")})
					case "image":
						if source, ok := m["source"].(map[string]interface{}); ok {
							mimeType := getString(source, "media_type")
							data := getString(source, "data")
							if mimeType != "" && data != "" {
								parts = append(parts, model.ContentPart{
									Type:     "image_url",
									ImageURL: &model.ImageURL{URL: fmt.Sprintf("data:%s;base64,%s", mimeType, data)},
								})
							}
						}
					case "thinking":
						reasoning += getString(m, "thinking")
					}
				}
			}
			outMsg := model.Message{Role: msg.Role, ReasoningContent: reasoning}
			if len(parts) == 1 && parts[0].Type == "text" {
				outMsg.Content = parts[0].Text
			} else {
				outMsg.Content = parts
			}
			oaiReq.Messages = append(oaiReq.Messages, outMsg)
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
		text := extractClaudeOutputText(msg.Content)
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

func extractClaudeOutputText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return normalizeJSONTextBlocks(v)
	case []model.ContentPart:
		var parts []string
		for _, part := range v {
			if part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "")
	case []interface{}:
		var parts []string
		for _, part := range v {
			if m, ok := part.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		return fmt.Sprintf("%v", v)
	}
}

func normalizeJSONTextBlocks(text string) string {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "[") && !strings.HasPrefix(trimmed, "{") {
		return text
	}
	var blocks []map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &blocks); err == nil {
		var parts []string
		for _, block := range blocks {
			if block["type"] == "text" || block["type"] == "output_text" {
				if part, ok := block["text"].(string); ok {
					parts = append(parts, part)
				}
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, "")
		}
	}
	return text
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
			Text             string                `json:"text,omitempty"`
			InlineData       *geminiInlineData     `json:"inlineData,omitempty"`
			FunctionCall     *geminiFunctionCall   `json:"functionCall,omitempty"`
			FunctionResponse *geminiFunctionResult `json:"functionResponse,omitempty"`
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

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResult struct {
	Name     string      `json:"name"`
	Response interface{} `json:"response"`
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

	toolCallIDs := map[string]string{}
	for _, content := range req.Contents {
		role := content.Role
		if role == "model" {
			role = "assistant"
		}
		if role == "" {
			role = "user"
		}
		var textParts []string
		var contentParts []model.ContentPart
		var toolCalls []model.ToolCall
		emittedToolResponse := false
		for _, p := range content.Parts {
			if p.Text != "" {
				textParts = append(textParts, p.Text)
				contentParts = append(contentParts, model.ContentPart{Type: "text", Text: p.Text})
			}
			if p.InlineData != nil && p.InlineData.MimeType != "" && p.InlineData.Data != "" {
				contentParts = append(contentParts, model.ContentPart{
					Type:     "image_url",
					ImageURL: &model.ImageURL{URL: fmt.Sprintf("data:%s;base64,%s", p.InlineData.MimeType, p.InlineData.Data)},
				})
			}
			if p.FunctionCall != nil {
				args, _ := json.Marshal(p.FunctionCall.Args)
				if len(args) == 0 || string(args) == "null" {
					args = []byte("{}")
				}
				callID := fmt.Sprintf("call_%d", len(toolCalls))
				if p.FunctionCall.Name != "" {
					callID = fmt.Sprintf("call_%s_%d", p.FunctionCall.Name, len(toolCalls))
					toolCallIDs[p.FunctionCall.Name] = callID
				}
				toolCalls = append(toolCalls, model.ToolCall{
					ID:   callID,
					Type: "function",
					Function: model.FunctionCall{
						Name:      p.FunctionCall.Name,
						Arguments: string(args),
					},
				})
			}
			if p.FunctionResponse != nil {
				body, _ := json.Marshal(p.FunctionResponse.Response)
				if len(body) == 0 || string(body) == "null" {
					body = []byte("{}")
				}
				toolCallID := toolCallIDs[p.FunctionResponse.Name]
				if toolCallID == "" {
					toolCallID = p.FunctionResponse.Name
				}
				oaiReq.Messages = append(oaiReq.Messages, model.Message{
					Role:       "tool",
					Name:       p.FunctionResponse.Name,
					ToolCallID: toolCallID,
					Content:    string(body),
				})
				emittedToolResponse = true
			}
		}
		if len(toolCalls) > 0 {
			msg := model.Message{Role: "assistant", ToolCalls: toolCalls}
			if len(textParts) > 0 {
				msg.Content = strings.Join(textParts, "")
			}
			oaiReq.Messages = append(oaiReq.Messages, msg)
			continue
		}
		if len(contentParts) > 1 || (len(contentParts) == 1 && contentParts[0].Type != "text") {
			oaiReq.Messages = append(oaiReq.Messages, model.Message{Role: role, Content: contentParts})
			continue
		}
		if len(textParts) == 0 && emittedToolResponse {
			continue
		}
		if len(textParts) == 0 && len(contentParts) == 0 {
			continue
		}
		oaiReq.Messages = append(oaiReq.Messages, model.Message{Role: role, Content: strings.Join(textParts, "")})
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
	var parts []map[string]interface{}
	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		msg := resp.Choices[0].Message
		if text := extractGeminiOutputText(msg.Content); text != "" {
			parts = append(parts, map[string]interface{}{"text": text})
		}
		for _, tc := range msg.ToolCalls {
			args := map[string]interface{}{}
			if tc.Function.Arguments != "" {
				_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
			}
			parts = append(parts, map[string]interface{}{
				"functionCall": map[string]interface{}{
					"name": tc.Function.Name,
					"args": args,
				},
			})
		}
	}
	if len(parts) == 0 {
		parts = append(parts, map[string]interface{}{"text": ""})
	}
	stopReason := "STOP"
	if len(resp.Choices) > 0 && resp.Choices[0].FinishReason != nil {
		stopReason = mapFinishReasonToGemini(*resp.Choices[0].FinishReason)
	}
	return map[string]interface{}{
		"candidates": []map[string]interface{}{{
			"content": map[string]interface{}{
				"parts": parts,
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

func extractGeminiOutputText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return normalizeJSONTextBlocks(v)
	case []model.ContentPart:
		var parts []string
		for _, part := range v {
			if part.Type == "text" {
				parts = append(parts, part.Text)
			}
		}
		return strings.Join(parts, "")
	case []interface{}:
		var parts []string
		for _, part := range v {
			if m, ok := part.(map[string]interface{}); ok {
				if text, ok := m["text"].(string); ok {
					parts = append(parts, text)
				}
			}
		}
		return strings.Join(parts, "")
	default:
		if content == nil {
			return ""
		}
		return fmt.Sprintf("%v", content)
	}
}

// Helpers

func mapFinishReasonToClaude(reason string) string {
	switch strings.ToLower(reason) {
	case "stop":
		return "end_turn"
	case "stop_sequence":
		return "stop_sequence"
	case "length", "max_tokens":
		return "max_tokens"
	case "tool_calls":
		return "tool_use"
	case "content_filter", "refusal":
		return "refusal"
	default:
		return reason
	}
}

func mapFinishReasonToGemini(reason string) string {
	switch strings.ToLower(reason) {
	case "stop":
		return "STOP"
	case "length", "max_tokens":
		return "MAX_TOKENS"
	case "content_filter", "refusal":
		return "SAFETY"
	case "tool_calls", "function_call":
		return "FUNCTION_CALL"
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
