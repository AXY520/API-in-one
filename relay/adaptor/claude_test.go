package adaptor

import (
	"api-in-one/model"
	"bufio"
	"io"
	"strings"
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

	if len(got.Messages) != 3 {
		t.Fatalf("expected placeholder user + assistant + tool result messages, got %d", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Fatalf("expected first message placeholder user, got %#v", got.Messages[0])
	}

	assistantBlocks, ok := got.Messages[1].Content.([]claudeContent)
	if !ok {
		t.Fatalf("expected assistant content blocks, got %T", got.Messages[1].Content)
	}
	if len(assistantBlocks) != 2 {
		t.Fatalf("expected text + tool_use blocks, got %#v", assistantBlocks)
	}
	if assistantBlocks[1].Type != "tool_use" || assistantBlocks[1].ID != "toolu_1" || assistantBlocks[1].Name != "Bash" {
		t.Fatalf("unexpected tool_use block: %#v", assistantBlocks[1])
	}

	userBlocks, ok := got.Messages[2].Content.([]claudeContent)
	if !ok {
		t.Fatalf("expected user content blocks, got %T", got.Messages[2].Content)
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

func TestClaudeConvertResponseNormalizesJSONTextBlocks(t *testing.T) {
	ad := &ClaudeAdaptor{}
	resp := &claudeResponse{
		ID:         "msg_1",
		Model:      "mimo-v2.5-pro",
		StopReason: "end_turn",
		Content: []claudeContent{{
			Type: "text",
			Text: `[{"type":"text","text":"All changes are complete."}]`,
		}},
	}

	got := ad.convertResponse(resp)

	if got.Choices[0].Message.Content != "All changes are complete." {
		t.Fatalf("unexpected content: %#v", got.Choices[0].Message.Content)
	}
}

func TestExtractTextContentHandlesContentParts(t *testing.T) {
	got := extractTextContent([]model.ContentPart{
		{Type: "text", Text: "hello"},
		{Type: "text", Text: " world"},
	})

	if got != "hello world" {
		t.Fatalf("unexpected text: %q", got)
	}
}

func TestBuildClaudeURLAvoidsDuplicateV1(t *testing.T) {
	cases := map[string]string{
		"https://example.com":             "https://example.com/v1/messages",
		"https://example.com/v1":          "https://example.com/v1/messages",
		"https://example.com/v1/":         "https://example.com/v1/messages",
		"https://example.com/v1/messages": "https://example.com/v1/messages",
		"https://example.com/messages":    "https://example.com/messages",
	}
	for input, want := range cases {
		if got := BuildClaudeURL(input); got != want {
			t.Fatalf("BuildClaudeURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestClaudeSSEProcessorCarriesMessageIDAndModelAcrossChunks(t *testing.T) {
	stream := strings.Join([]string{
		`event: message_start`,
		`data: {"type":"message_start","message":{"id":"msg_123","model":"claude-test"}}`,
		``,
		`event: content_block_delta`,
		`data: {"type":"content_block_delta","delta":{"type":"thinking_delta","thinking":"pong"}}`,
		``,
		`event: message_delta`,
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"}}`,
		``,
	}, "\n")

	body := &fakeHTTPResponseBody{Reader: strings.NewReader(stream)}
	p := &claudeSSEProcessor{
		scanner: bufio.NewScanner(body),
		body:    body,
	}

	first, err := p.Next()
	if err != nil {
		t.Fatalf("first chunk: %v", err)
	}
	if !strings.Contains(string(first), `"id":"msg_123"`) || !strings.Contains(string(first), `"model":"claude-test"`) {
		t.Fatalf("first chunk missing id/model: %s", string(first))
	}

	second, err := p.Next()
	if err != nil {
		t.Fatalf("second chunk: %v", err)
	}
	if !strings.Contains(string(second), `"id":"msg_123"`) || !strings.Contains(string(second), `"model":"claude-test"`) {
		t.Fatalf("second chunk missing carried id/model: %s", string(second))
	}
	if !strings.Contains(string(second), `"reasoning_content":"pong"`) {
		t.Fatalf("second chunk missing reasoning delta: %s", string(second))
	}

	third, err := p.Next()
	if err != nil {
		t.Fatalf("third chunk: %v", err)
	}
	if !strings.Contains(string(third), `"id":"msg_123"`) || !strings.Contains(string(third), `"model":"claude-test"`) {
		t.Fatalf("third chunk missing carried id/model: %s", string(third))
	}
	if !strings.Contains(string(third), `"finish_reason":"stop"`) {
		t.Fatalf("third chunk missing finish reason: %s", string(third))
	}

	if _, err := p.Next(); err != io.EOF {
		t.Fatalf("expected EOF, got %v", err)
	}
}

type fakeHTTPResponseBody struct {
	*strings.Reader
}

func (b *fakeHTTPResponseBody) Close() error { return nil }
