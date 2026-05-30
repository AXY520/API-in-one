package adaptor

import (
	"api-in-one/model"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// ClaudeAdaptor handles Anthropic Claude API format conversion.
type ClaudeAdaptor struct{}

// ---- Claude native request/response structures ----

type claudeRequest struct {
	Model     string          `json:"model"`
	MaxTokens int             `json:"max_tokens"`
	System    string          `json:"system,omitempty"`
	Messages  []claudeMessage `json:"messages"`
	Stream    bool            `json:"stream,omitempty"`
	Tools     []claudeTool    `json:"tools,omitempty"`
}

type claudeMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // string or []claudeContent
}

type claudeContent struct {
	Type      string      `json:"type"`
	Text      string      `json:"text,omitempty"`
	Thinking  string      `json:"thinking,omitempty"`
	ID        string      `json:"id,omitempty"`
	Name      string      `json:"name,omitempty"`
	Input     interface{} `json:"input,omitempty"`
	ToolUseID string      `json:"tool_use_id,omitempty"`
	Content   interface{} `json:"content,omitempty"`
}

type claudeTool struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema"`
}

type claudeResponse struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	Role       string          `json:"role"`
	Content    []claudeContent `json:"content"`
	Model      string          `json:"model"`
	StopReason string          `json:"stop_reason"`
	Usage      claudeUsage     `json:"usage"`
}

type claudeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func (a *ClaudeAdaptor) Name() string { return "claude" }

func (a *ClaudeAdaptor) BuildHTTPRequest(baseURL, key string, req *model.ChatCompletionRequest) (*http.Request, error) {
	claudeReq := a.convertRequest(req)
	body, err := json.Marshal(claudeReq)
	if err != nil {
		return nil, err
	}
	url := buildClaudeURL(baseURL)
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-api-key", key)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	return httpReq, nil
}

func (a *ClaudeAdaptor) convertRequest(req *model.ChatCompletionRequest) *claudeRequest {
	cr := &claudeRequest{
		Model:     req.Model,
		MaxTokens: 4096, // Claude requires max_tokens
		Stream:    req.Stream,
	}
	if req.MaxTokens != nil && *req.MaxTokens > 0 {
		cr.MaxTokens = *req.MaxTokens
	}

	// Extract system message and convert messages
	var systemParts []string
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system":
			content := extractTextContent(msg.Content)
			systemParts = append(systemParts, content)
		case "tool":
			toolResult := claudeContent{
				Type:      "tool_result",
				ToolUseID: msg.ToolCallID,
				Content:   extractTextContent(msg.Content),
			}
			cr.Messages = append(cr.Messages, claudeMessage{
				Role:    "user",
				Content: []claudeContent{toolResult},
			})
		case "assistant":
			content := openAIMessageToClaudeContent(msg)
			cr.Messages = append(cr.Messages, claudeMessage{
				Role:    "assistant",
				Content: content,
			})
		default:
			claudeMsg := claudeMessage{
				Role:    msg.Role,
				Content: extractTextContent(msg.Content),
			}
			cr.Messages = append(cr.Messages, claudeMsg)
		}
	}
	if len(systemParts) > 0 {
		cr.System = strings.Join(systemParts, "\n")
	}

	// Convert tools
	for _, tool := range req.Tools {
		ct := claudeTool{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			InputSchema: tool.Function.Parameters,
		}
		cr.Tools = append(cr.Tools, ct)
	}

	return cr
}

func (a *ClaudeAdaptor) ParseResponse(resp *http.Response) (*model.ChatCompletionResponse, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("claude upstream error (status %d): %s", resp.StatusCode, string(body))
	}
	var claudeResp claudeResponse
	if err := json.Unmarshal(body, &claudeResp); err != nil {
		return nil, err
	}
	return a.convertResponse(&claudeResp), nil
}

func (a *ClaudeAdaptor) convertResponse(cr *claudeResponse) *model.ChatCompletionResponse {
	var textParts []string
	var thinkingParts []string
	var toolCalls []model.ToolCall
	for _, c := range cr.Content {
		switch c.Type {
		case "text":
			textParts = append(textParts, c.Text)
		case "thinking":
			thinkingParts = append(thinkingParts, c.Thinking)
		case "tool_use":
			args, _ := json.Marshal(c.Input)
			toolCalls = append(toolCalls, model.ToolCall{
				ID:   c.ID,
				Type: "function",
				Function: model.FunctionCall{
					Name:      c.Name,
					Arguments: string(args),
				},
			})
		}
	}
	finishReason := mapStopReason(cr.StopReason)
	msg := &model.Message{
		Role:    "assistant",
		Content: strings.Join(textParts, ""),
	}
	if len(thinkingParts) > 0 {
		msg.ReasoningContent = strings.Join(thinkingParts, "")
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return &model.ChatCompletionResponse{
		ID:      cr.ID,
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   cr.Model,
		Choices: []model.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: &finishReason,
		}},
		Usage: model.Usage{
			PromptTokens:     cr.Usage.InputTokens,
			CompletionTokens: cr.Usage.OutputTokens,
			TotalTokens:      cr.Usage.InputTokens + cr.Usage.OutputTokens,
		},
	}
}

func openAIMessageToClaudeContent(msg model.Message) interface{} {
	var content []claudeContent
	text := extractTextContent(msg.Content)
	if text != "" {
		content = append(content, claudeContent{Type: "text", Text: text})
	}
	for _, tc := range msg.ToolCalls {
		input := map[string]interface{}{}
		if tc.Function.Arguments != "" {
			json.Unmarshal([]byte(tc.Function.Arguments), &input)
		}
		content = append(content, claudeContent{
			Type:  "tool_use",
			ID:    tc.ID,
			Name:  tc.Function.Name,
			Input: input,
		})
	}
	if len(content) == 0 {
		return ""
	}
	return content
}

func (a *ClaudeAdaptor) StreamHandler(resp *http.Response) SSEProcessor {
	return &claudeSSEProcessor{
		scanner: bufio.NewScanner(resp.Body),
		body:    resp.Body,
	}
}

// ---- Claude SSE Processor ----

type claudeSSEProcessor struct {
	scanner  *bufio.Scanner
	body     io.ReadCloser
	finished bool
}

func (p *claudeSSEProcessor) Next() ([]byte, error) {
	if p.finished {
		return nil, io.EOF
	}
	for p.scanner.Scan() {
		line := p.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var event map[string]interface{}
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			continue
		}

		eventType, _ := event["type"].(string)

		switch eventType {
		case "message_start":
			msg, _ := event["message"].(map[string]interface{})
			modelName, _ := msg["model"].(string)
			msgID, _ := msg["id"].(string)
			chunk := model.ChatCompletionChunk{
				ID:      msgID,
				Object:  "chat.completion.chunk",
				Created: time.Now().Unix(),
				Model:   modelName,
				Choices: []model.ChunkChoice{{
					Index: 0,
					Delta: &model.Message{Role: "assistant", Content: ""},
				}},
			}
			return marshalSSE(chunk)

		case "content_block_start":
			cb, _ := event["content_block"].(map[string]interface{})
			cbType, _ := cb["type"].(string)
			if cbType == "tool_use" {
				// Emit a chunk with tool_call info
				callID, _ := cb["id"].(string)
				name, _ := cb["name"].(string)
				idx, _ := event["index"].(float64)
				chunk := model.ChatCompletionChunk{
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Choices: []model.ChunkChoice{{
						Index: 0,
						Delta: &model.Message{
							ToolCalls: []model.ToolCall{{
								Index:    int(idx),
								ID:       callID,
								Type:     "function",
								Function: model.FunctionCall{Name: name},
							}},
						},
					}},
				}
				return marshalSSE(chunk)
			}

		case "content_block_delta":
			delta, _ := event["delta"].(map[string]interface{})
			deltaType, _ := delta["type"].(string)
			switch deltaType {
			case "thinking_delta":
				thinking, _ := delta["thinking"].(string)
				if thinking != "" {
					chunk := model.ChatCompletionChunk{
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Choices: []model.ChunkChoice{{
							Index: 0,
							Delta: &model.Message{ReasoningContent: thinking},
						}},
					}
					return marshalSSE(chunk)
				}
			case "input_json_delta":
				partialJSON, _ := delta["partial_json"].(string)
				idx, _ := event["index"].(float64)
				if partialJSON != "" {
					chunk := model.ChatCompletionChunk{
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Choices: []model.ChunkChoice{{
							Index: 0,
							Delta: &model.Message{
								ToolCalls: []model.ToolCall{{
									Index:    int(idx),
									Function: model.FunctionCall{Arguments: partialJSON},
								}},
							},
						}},
					}
					return marshalSSE(chunk)
				}
			default:
				text, _ := delta["text"].(string)
				if text != "" {
					chunk := model.ChatCompletionChunk{
						Object:  "chat.completion.chunk",
						Created: time.Now().Unix(),
						Choices: []model.ChunkChoice{{
							Index: 0,
							Delta: &model.Message{Content: text},
						}},
					}
					return marshalSSE(chunk)
				}
			}

		case "message_delta":
			delta, _ := event["delta"].(map[string]interface{})
			stopReason, _ := delta["stop_reason"].(string)
			if stopReason != "" {
				fr := mapStopReason(stopReason)
				chunk := model.ChatCompletionChunk{
					Object:  "chat.completion.chunk",
					Created: time.Now().Unix(),
					Choices: []model.ChunkChoice{{
						Index:        0,
						Delta:        &model.Message{},
						FinishReason: &fr,
					}},
				}
				p.finished = true
				return marshalSSE(chunk)
			}

		case "message_stop":
			p.finished = true
			return nil, io.EOF
		}
	}
	if err := p.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

// ---- Helpers ----

func extractTextContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, part := range v {
			if m, ok := part.(map[string]interface{}); ok {
				if m["type"] == "text" {
					if text, ok := m["text"].(string); ok {
						parts = append(parts, text)
					}
				}
			}
		}
		return strings.Join(parts, "")
	default:
		b, _ := json.Marshal(content)
		return string(b)
	}
}

func mapStopReason(reason string) string {
	switch reason {
	case "end_turn":
		return "stop"
	case "max_tokens":
		return "length"
	case "tool_use":
		return "tool_calls"
	default:
		return "stop"
	}
}

func marshalSSE(chunk model.ChatCompletionChunk) ([]byte, error) {
	b, err := json.Marshal(chunk)
	if err != nil {
		return nil, err
	}
	return []byte("data: " + string(b) + "\n\n"), nil
}

// buildClaudeURL constructs the Claude API endpoint URL.
// If the base URL already ends with a path that looks like an endpoint,
// use it directly. Otherwise append /v1/messages.
func buildClaudeURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	// If it already ends with /messages or /v1/messages, use as-is
	if strings.HasSuffix(baseURL, "/messages") {
		return baseURL
	}
	return baseURL + "/v1/messages"
}
