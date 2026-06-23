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
	// These are now tested in adaptor package. Keep this test for coverage.
}

func TestDoConvertedAnyPrefersNativeClaudeChannel(t *testing.T) {
	var gotPath string
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "public-model",
			"content":       []map[string]interface{}{{"type": "text", "text": "ok"}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]interface{}{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	claude := model.NewChannel("claude", "claude", "", server.URL+"/v1", "", false, []string{"claude-key"}, []string{"public-model"}, nil, 10, 100)
	engine := NewEngine(NewPool([]*model.Channel{claude}))

	result, err := engine.DoConvertedAny(context.Background(), &model.ChatCompletionRequest{
		Model: "public-model",
		Messages: []model.Message{{
			Role:    "user",
			Content: "hello",
		}},
	}, "claude")
	if err != nil {
		t.Fatalf("DoConvertedAny: %v", err)
	}
	if result.Channel != "claude" {
		t.Fatalf("expected mixed routing to prefer first channel, got %s", result.Channel)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("expected Claude messages path, got %s", gotPath)
	}
	if gotKey != "claude-key" {
		t.Fatalf("expected Claude x-api-key, got %q", gotKey)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].Protocol != "claude" || result.Attempts[0].AdaptorName != "claude" {
		t.Fatalf("unexpected attempts: %#v", result.Attempts)
	}
	if result.Attempts[0].UpstreamProtocol != "claude" || result.Attempts[0].ConversionMode != "passthrough" || result.Attempts[0].UpstreamURL == "" {
		t.Fatalf("missing upstream attempt metadata: %#v", result.Attempts[0])
	}
}

func TestDoConvertedAnyCanUseMappedOpenAICompatibleChannel(t *testing.T) {
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

	openai := model.NewChannel("openai", "openai", server.URL+"/v1", "", "", false, []string{"openai-key"}, []string{"upstream-model"}, map[string]string{"public-model": "upstream-model"}, 10, 100)
	engine := NewEngine(NewPool([]*model.Channel{openai}))

	result, err := engine.DoConvertedAny(context.Background(), &model.ChatCompletionRequest{
		Model: "public-model",
		Messages: []model.Message{{
			Role:    "user",
			Content: "hello",
		}},
	}, "claude")
	if err != nil {
		t.Fatalf("DoConvertedAny: %v", err)
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
	if result.Attempts[0].UpstreamProtocol != "openai" || result.Attempts[0].ConversionMode != "converted" || result.Attempts[0].UpstreamURL == "" {
		t.Fatalf("missing upstream attempt metadata: %#v", result.Attempts[0])
	}
}

func TestDoConvertedAnyUsesClaudeURLForChatInbound(t *testing.T) {
	var gotPath string
	var gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "deepseek-v4-flash",
			"content":       []map[string]interface{}{{"type": "text", "text": "ok"}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]interface{}{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer server.Close()

	claudeOnly := model.NewChannel("openmodel", "openai", "", server.URL+"/v1", "", false, []string{"om-key"}, []string{"deepseek-v4-flash"}, nil, 10, 100)
	engine := NewEngine(NewPool([]*model.Channel{claudeOnly}))

	result, err := engine.DoConvertedAny(context.Background(), &model.ChatCompletionRequest{
		Model: "deepseek-v4-flash",
		Messages: []model.Message{{
			Role:    "user",
			Content: "hello",
		}},
	}, "openai")
	if err != nil {
		t.Fatalf("DoConvertedAny: %v", err)
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("expected Claude messages path, got %s", gotPath)
	}
	if gotKey != "om-key" {
		t.Fatalf("expected Claude x-api-key, got %q", gotKey)
	}
	if result.Channel != "openmodel" || result.Response == nil || result.Response.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected result: %#v", result)
	}
	if len(result.Attempts) != 1 || result.Attempts[0].InboundProtocol != "openai" || result.Attempts[0].UpstreamProtocol != "claude" || result.Attempts[0].ConversionMode != "converted" {
		t.Fatalf("unexpected attempts: %#v", result.Attempts)
	}
}
