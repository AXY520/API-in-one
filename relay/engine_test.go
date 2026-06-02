package relay

import (
	"api-in-one/model"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSanitizeRequestPreservesToolMessagesForOpenAI(t *testing.T) {
	req := &model.ChatCompletionRequest{
		Model: "m",
		Messages: []model.Message{
			{
				Role:    "assistant",
				Content: "",
				ToolCalls: []model.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "Bash",
						Arguments: `{"cmd":"pwd"}`,
					},
				}},
			},
			{
				Role:       "tool",
				ToolCallID: "call_1",
				Content:    "/tmp/project",
			},
		},
	}

	sanitizeRequest(req, true)

	if len(req.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(req.Messages))
	}
	if len(req.Messages[0].ToolCalls) != 1 {
		t.Fatalf("expected assistant tool_calls to be preserved")
	}
	if req.Messages[1].Role != "tool" {
		t.Fatalf("expected tool role to be preserved, got %q", req.Messages[1].Role)
	}
	if req.Messages[1].ToolCallID != "call_1" {
		t.Fatalf("expected tool_call_id to be preserved")
	}
}

func TestSanitizeRequestDowngradesToolMessagesWhenNeeded(t *testing.T) {
	req := &model.ChatCompletionRequest{
		Model: "m",
		Messages: []model.Message{
			{
				Role: "assistant",
				ToolCalls: []model.ToolCall{{
					ID:   "call_1",
					Type: "function",
					Function: model.FunctionCall{
						Name:      "Bash",
						Arguments: `{"cmd":"pwd"}`,
					},
				}},
			},
			{
				Role:       "tool",
				ToolCallID: "call_1",
				Content:    "/tmp/project",
			},
		},
	}

	sanitizeRequest(req, false)

	if len(req.Messages) != 1 {
		t.Fatalf("expected empty assistant tool call to be dropped and tool result to remain, got %d", len(req.Messages))
	}
	if req.Messages[0].Role != "user" {
		t.Fatalf("expected downgraded tool result as user, got %q", req.Messages[0].Role)
	}
	if req.Messages[0].Content != "[Tool Result for call_1]\n/tmp/project" {
		t.Fatalf("unexpected downgraded content: %#v", req.Messages[0].Content)
	}
}

func TestBuildRawClaudeURLAvoidsDuplicateV1(t *testing.T) {
	cases := map[string]string{
		"https://example.com":             "https://example.com/v1/messages",
		"https://example.com/v1":          "https://example.com/v1/messages",
		"https://example.com/v1/":         "https://example.com/v1/messages",
		"https://example.com/v1/messages": "https://example.com/v1/messages",
		"https://example.com/messages":    "https://example.com/messages",
	}
	for input, want := range cases {
		if got := buildRawClaudeURL(input); got != want {
			t.Fatalf("buildRawClaudeURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestBuildRawChatCompletionsURLAvoidsDuplicateV1(t *testing.T) {
	cases := map[string]string{
		"https://example.com":                     "https://example.com/v1/chat/completions",
		"https://example.com/v1":                  "https://example.com/v1/chat/completions",
		"https://example.com/v1/":                 "https://example.com/v1/chat/completions",
		"https://example.com/v1/chat/completions": "https://example.com/v1/chat/completions",
	}
	for input, want := range cases {
		if got := buildRawChatCompletionsURL(input); got != want {
			t.Fatalf("buildRawChatCompletionsURL(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestDoConvertedUsesOpenAICompatibleChannel(t *testing.T) {
	var gotPath string
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.ChatCompletionResponse{
			ID:      "chatcmpl_test",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "upstream-model",
			Choices: []model.Choice{{
				Index: 0,
				Message: &model.Message{
					Role:    "assistant",
					Content: "ok",
				},
			}},
		})
	}))
	defer server.Close()

	openai := model.NewChannel("openai", "openai", server.URL+"/v1", "", "", false, []string{"openai-key"}, []string{"public-model"}, map[string]string{"public-model": "upstream-model"}, 10, 100)
	claude := model.NewChannel("claude", "claude", "https://claude.example", "https://claude.example/v1", "", false, []string{"claude-key"}, []string{"public-model"}, nil, 10, 100)
	engine := NewEngine(NewPool([]*model.Channel{claude, openai}))

	result, err := engine.DoConverted(context.Background(), &model.ChatCompletionRequest{
		Model: "public-model",
		Messages: []model.Message{{
			Role:    "user",
			Content: "hello",
		}},
	}, "claude")
	if err != nil {
		t.Fatalf("DoConverted: %v", err)
	}
	if result.Channel != "openai" {
		t.Fatalf("expected openai channel, got %s", result.Channel)
	}
	if result.Model != "upstream-model" {
		t.Fatalf("expected resolved model upstream-model, got %s", result.Model)
	}
	if gotPath != "/v1/chat/completions" {
		t.Fatalf("expected OpenAI chat completions path, got %s", gotPath)
	}
	if gotAuth != "Bearer openai-key" {
		t.Fatalf("expected OpenAI bearer auth, got %q", gotAuth)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Protocol != "claude" || result.Attempts[0].AdaptorName != "openai" {
		t.Fatalf("unexpected attempts: %#v", result.Attempts)
	}
}
