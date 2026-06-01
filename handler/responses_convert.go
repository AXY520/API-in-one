package handler

import (
	"api-in-one/model"
	"fmt"
	"strings"
)

type responsesInboundRequest struct {
	Model             string        `json:"model"`
	Input             interface{}   `json:"input"`
	Instructions      string        `json:"instructions"`
	Stream            bool          `json:"stream"`
	MaxOutputTokens   int           `json:"max_output_tokens"`
	Temperature       *float64      `json:"temperature"`
	TopP              *float64      `json:"top_p"`
	Tools             []interface{} `json:"tools,omitempty"`
	ToolChoice        interface{}   `json:"tool_choice,omitempty"`
	ParallelToolCalls *bool         `json:"parallel_tool_calls,omitempty"`
}

func responsesToChatCompletion(req *responsesInboundRequest, enableMiMoCompat bool) *model.ChatCompletionRequest {
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

	for _, t := range req.Tools {
		if tool, ok := responsesToolToChatTool(t, enableMiMoCompat); ok {
			oaiReq.Tools = append(oaiReq.Tools, tool)
		}
	}

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

	switch v := req.Input.(type) {
	case string:
		oaiReq.Messages = append(oaiReq.Messages, model.Message{
			Role:    "user",
			Content: v,
		})
	case []interface{}:
		appendResponsesInputItems(oaiReq, v, enableMiMoCompat)
	}

	if enableMiMoCompat {
		oaiReq.Messages = mergeConsecutiveRoles(oaiReq.Messages)
	}

	return oaiReq
}

func responsesToolToChatTool(t interface{}, enableMiMoCompat bool) (model.Tool, bool) {
	tm, ok := t.(map[string]interface{})
	if !ok {
		return model.Tool{}, false
	}
	toolType, _ := tm["type"].(string)
	switch toolType {
	case "function":
		name, _ := tm["name"].(string)
		if name == "" {
			return model.Tool{}, false
		}
		oaiTool := model.Tool{Type: "function"}
		oaiTool.Function.Name = name
		if desc, ok := tm["description"].(string); ok {
			oaiTool.Function.Description = desc
		}
		if params, ok := tm["parameters"]; ok {
			oaiTool.Function.Parameters = params
		}
		return oaiTool, true
	case "custom", "tool_search":
		if !enableMiMoCompat && toolType == "custom" {
			return model.Tool{}, false
		}
		name := toolType
		if rawName, ok := tm["name"].(string); ok && rawName != "" {
			name = rawName
		}
		oaiTool := model.Tool{Type: "function"}
		oaiTool.Function.Name = name
		if desc, ok := tm["description"].(string); ok {
			oaiTool.Function.Description = desc
		}
		if params, ok := tm["parameters"]; ok {
			oaiTool.Function.Parameters = params
		} else {
			oaiTool.Function.Parameters = map[string]interface{}{
				"type":                 "object",
				"additionalProperties": true,
			}
		}
		return oaiTool, true
	case "local_shell":
		if !enableMiMoCompat {
			return model.Tool{}, false
		}
		return localShellTool(), true
	default:
		return model.Tool{}, false
	}
}

func localShellTool() model.Tool {
	return model.Tool{
		Type: "function",
		Function: model.ToolFunction{
			Name:        "shell",
			Description: "Execute a shell command on the local machine. Returns stdout, stderr and exit code.",
			Parameters: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"command": map[string]interface{}{
						"type":        "array",
						"items":       map[string]interface{}{"type": "string"},
						"description": `Argv array, e.g. ["ls", "-la"].`,
					},
					"workdir": map[string]interface{}{
						"type":        "string",
						"description": "Working directory to run the command in.",
					},
					"timeout_ms": map[string]interface{}{
						"type":        "number",
						"description": "Timeout in milliseconds.",
					},
				},
				"required": []string{"command"},
			},
		},
	}
}

func appendResponsesInputItems(oaiReq *model.ChatCompletionRequest, items []interface{}, enableMiMoCompat bool) {
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

	for _, item := range items {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		itemType, _ := m["type"].(string)

		switch itemType {
		case "reasoning":
			if !enableMiMoCompat {
				continue
			}
			if encrypted := responsesReasoningContent(m); encrypted != "" {
				pendingReasoning += encrypted
			}

		case "function_call":
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
				if s, ok := extractContentText(m["content"]).(string); ok {
					pendingText = s
				}
				continue
			}
			flushPending()
			msg := model.Message{Role: role}
			msg.Content = extractContentText(m["content"])
			oaiReq.Messages = append(oaiReq.Messages, msg)

		case "function_call_output":
			flushPending()
			oaiReq.Messages = append(oaiReq.Messages, model.Message{
				Role:       "tool",
				ToolCallID: getString(m, "call_id"),
				Content:    getString(m, "output"),
			})

		default:
			flushPending()
			role, _ := m["role"].(string)
			if role != "" {
				msg := model.Message{Role: role}
				msg.Content = extractContentText(m["content"])
				oaiReq.Messages = append(oaiReq.Messages, msg)
			}
		}
	}
	flushPending()
}

func responsesReasoningContent(item map[string]interface{}) string {
	encrypted, _ := item["encrypted_content"].(string)
	if encrypted != "" {
		return encrypted
	}
	var text string
	if summary, ok := item["summary"].([]interface{}); ok {
		for _, s := range summary {
			sm, ok := s.(map[string]interface{})
			if !ok || sm["type"] != "summary_text" {
				continue
			}
			text += getString(sm, "text")
		}
	}
	return text
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

func chatCompletionToResponses(resp *model.ChatCompletionResponse, modelName string, enableMiMoCompat bool) map[string]interface{} {
	var output []map[string]interface{}

	if len(resp.Choices) > 0 && resp.Choices[0].Message != nil {
		msg := resp.Choices[0].Message

		if enableMiMoCompat && msg.ReasoningContent != "" {
			output = append(output, map[string]interface{}{
				"type":              "reasoning",
				"id":                fmt.Sprintf("reason_%s", resp.ID),
				"summary":           []map[string]interface{}{{"type": "summary_text", "text": msg.ReasoningContent}},
				"encrypted_content": msg.ReasoningContent,
				"status":            "completed",
			})
		}

		text := ""
		if s, ok := msg.Content.(string); ok {
			text = s
		}
		var fakeCalls []fakeToolCall
		if enableMiMoCompat {
			text, fakeCalls = extractFakeToolCalls(text)
		}
		if strings.TrimSpace(text) != "" || len(msg.ToolCalls) == 0 && len(fakeCalls) == 0 {
			output = append(output, map[string]interface{}{
				"type":    "message",
				"id":      fmt.Sprintf("msg_%s", resp.ID),
				"role":    "assistant",
				"status":  "completed",
				"content": []map[string]interface{}{{"type": "output_text", "text": strings.TrimSpace(text), "annotations": []interface{}{}}},
			})
		}

		for i, call := range fakeCalls {
			callID := fmt.Sprintf("call_%s_%d", resp.ID, i)
			output = append(output, map[string]interface{}{
				"type":      "function_call",
				"id":        fmt.Sprintf("fc_%s", callID),
				"call_id":   callID,
				"name":      call.Name,
				"arguments": call.Arguments,
				"status":    "completed",
			})
		}

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
