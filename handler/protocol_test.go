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
