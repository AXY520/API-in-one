package adaptor

import (
	"api-in-one/model"
	"testing"
)

func TestClaudeConvertRequestPreservesToolTurns(t *testing.T) {
	ad := &ClaudeAdaptor{}
	req := &model.ChatCompletionRequest{
		Model: "claude-test",
		Messages: []model.Message{
			{
				Role:    "assistant",
				Content: "I will check.",
				ToolCalls: []model.ToolCall{{
					ID:   "toolu_1",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "Bash",
						Arguments: `{"cmd":"pwd"}`,
					},
				}},
			},
			{
				Role:       "tool",
				ToolCallID: "toolu_1",
				Content:    "/home/axy/project",
			},
		},
	}

	got := ad.convertRequest(req)

	if len(got.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got.Messages))
	}

	assistantBlocks, ok := got.Messages[0].Content.([]claudeContent)
	if !ok {
		t.Fatalf("expected assistant content blocks, got %T", got.Messages[0].Content)
	}
	if len(assistantBlocks) != 2 {
		t.Fatalf("expected text + tool_use blocks, got %#v", assistantBlocks)
	}
	if assistantBlocks[1].Type != "tool_use" || assistantBlocks[1].ID != "toolu_1" || assistantBlocks[1].Name != "Bash" {
		t.Fatalf("unexpected tool_use block: %#v", assistantBlocks[1])
	}

	userBlocks, ok := got.Messages[1].Content.([]claudeContent)
	if !ok {
		t.Fatalf("expected user content blocks, got %T", got.Messages[1].Content)
	}
	if len(userBlocks) != 1 || userBlocks[0].Type != "tool_result" || userBlocks[0].ToolUseID != "toolu_1" {
		t.Fatalf("unexpected tool_result block: %#v", userBlocks)
	}
}

func TestClaudeConvertResponsePreservesToolUse(t *testing.T) {
	ad := &ClaudeAdaptor{}
	resp := &claudeResponse{
		ID:         "msg_1",
		Model:      "claude-test",
		StopReason: "tool_use",
		Content: []claudeContent{{
			Type:  "tool_use",
			ID:    "toolu_1",
			Name:  "Bash",
			Input: map[string]interface{}{"cmd": "pwd"},
		}},
	}

	got := ad.convertResponse(resp)

	if len(got.Choices) != 1 || got.Choices[0].Message == nil {
		t.Fatalf("missing choice message")
	}
	msg := got.Choices[0].Message
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %#v", msg.ToolCalls)
	}
	if msg.ToolCalls[0].ID != "toolu_1" || msg.ToolCalls[0].Function.Name != "Bash" {
		t.Fatalf("unexpected tool call: %#v", msg.ToolCalls[0])
	}
	if got.Choices[0].FinishReason == nil || *got.Choices[0].FinishReason != "tool_calls" {
		t.Fatalf("expected tool_calls finish reason, got %#v", got.Choices[0].FinishReason)
	}
}
