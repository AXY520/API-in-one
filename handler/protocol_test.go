package handler

import (
	"api-in-one/model"
	"encoding/json"
	"strings"
	"testing"
)

func TestChatCompletionToResponsesConvertsFakeCodexToolCall(t *testing.T) {
	resp := &model.ChatCompletionResponse{
		ID:      "test",
		Created: 1,
		Choices: []model.Choice{{
			Message: &model.Message{
				Role: "assistant",
				Content: `• <tool_call>
  <function=update_plan>
  <parameter=explanation>先提取日志。</parameter>
  <parameter=[{"status":"in_progress","step":"提取日志"}]</parameter>
  </function>
  </tool_call>`,
			},
		}},
	}

	out := chatCompletionToResponses(resp, "mimo", true)
	items, ok := out["output"].([]map[string]interface{})
	if !ok {
		t.Fatalf("unexpected output: %#v", out["output"])
	}
	if len(items) != 1 {
		t.Fatalf("expected only function_call item, got %#v", items)
	}
	if items[0]["type"] != "function_call" || items[0]["name"] != "update_plan" {
		t.Fatalf("expected update_plan function_call, got %#v", items[0])
	}
	args, ok := items[0]["arguments"].(string)
	if !ok || !json.Valid([]byte(args)) {
		t.Fatalf("expected valid json arguments, got %#v", items[0]["arguments"])
	}
	if args == "{}" {
		t.Fatal("expected extracted arguments, got empty object")
	}
	var decoded map[string]interface{}
	if err := json.Unmarshal([]byte(args), &decoded); err != nil {
		t.Fatalf("decode args: %v", err)
	}
	if decoded["explanation"] != "先提取日志。" {
		t.Fatalf("missing explanation: %#v", decoded)
	}
	if _, ok := decoded["plan"].([]interface{}); !ok {
		t.Fatalf("missing plan array: %#v", decoded)
	}
}

func TestChatCompletionToResponsesDoesNotParseFakeToolCallForNormalModels(t *testing.T) {
	resp := &model.ChatCompletionResponse{
		ID:      "test",
		Created: 1,
		Choices: []model.Choice{{
			Message: &model.Message{
				Role:    "assistant",
				Content: `<tool_call><function=update_plan><parameter=explanation>text</parameter></function></tool_call>`,
			},
		}},
	}

	out := chatCompletionToResponses(resp, "gpt-compatible", false)
	items := out["output"].([]map[string]interface{})
	if len(items) != 1 || items[0]["type"] != "message" {
		t.Fatalf("expected normal message output, got %#v", items)
	}
	content := items[0]["content"].([]map[string]interface{})
	if content[0]["text"] == "" || !strings.Contains(content[0]["text"].(string), "<tool_call>") {
		t.Fatalf("expected original text to be preserved, got %#v", content)
	}
}

func TestChatCompletionToResponsesIncludesNormalText(t *testing.T) {
	resp := &model.ChatCompletionResponse{
		ID:      "text",
		Created: 1,
		Choices: []model.Choice{{
			Message: &model.Message{
				Role:    "assistant",
				Content: "这是实际回复内容",
			},
		}},
	}

	out := chatCompletionToResponses(resp, "gpt-compatible", false)
	items := out["output"].([]map[string]interface{})
	if len(items) != 1 || items[0]["type"] != "message" {
		t.Fatalf("expected message output, got %#v", items)
	}
	content := items[0]["content"].([]map[string]interface{})
	if content[0]["text"] != "这是实际回复内容" {
		t.Fatalf("missing response text: %#v", content)
	}
}

func TestResponsesToChatCompletionDropsReasoningForNormalModels(t *testing.T) {
	req := &responsesInboundRequest{
		Model: "gpt-compatible",
		Input: []interface{}{
			map[string]interface{}{
				"type":              "reasoning",
				"encrypted_content": "mimo-only reasoning",
			},
			map[string]interface{}{
				"type": "message",
				"role": "assistant",
				"content": []interface{}{
					map[string]interface{}{"type": "output_text", "text": "previous answer"},
				},
			},
			map[string]interface{}{
				"type":    "function_call_output",
				"call_id": "call_1",
				"output":  "result",
			},
		},
	}

	out := responsesToChatCompletion(req, false)
	for _, msg := range out.Messages {
		if strings.Contains(msg.ReasoningContent, "mimo-only") {
			t.Fatalf("normal model should not receive MiMo reasoning: %#v", out.Messages)
		}
	}
}

func TestResponsesToChatCompletionMapsLocalShellForMiMo(t *testing.T) {
	req := &responsesInboundRequest{
		Model: "mimo-v2.5-pro",
		Input: "hi",
		Tools: []interface{}{
			map[string]interface{}{"type": "local_shell"},
			map[string]interface{}{"type": "file_search"},
		},
	}

	out := responsesToChatCompletion(req, true)
	if len(out.Tools) != 1 {
		t.Fatalf("expected one converted tool, got %#v", out.Tools)
	}
	if out.Tools[0].Type != "function" || out.Tools[0].Function.Name != "shell" {
		t.Fatalf("expected local_shell to map to shell function, got %#v", out.Tools[0])
	}
}

func TestResponsesToChatCompletionMapsToolSearch(t *testing.T) {
	req := &responsesInboundRequest{
		Model: "mimo-v2.5-pro",
		Input: "hi",
		Tools: []interface{}{
			map[string]interface{}{
				"type":        "tool_search",
				"description": "Search deferred tool metadata.",
				"parameters": map[string]interface{}{
					"type": "object",
				},
			},
		},
	}

	out := responsesToChatCompletion(req, true)
	if len(out.Tools) != 1 || out.Tools[0].Function.Name != "tool_search" {
		t.Fatalf("expected tool_search function, got %#v", out.Tools)
	}
}
