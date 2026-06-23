package handler

import (
	"api-in-one/model"
	"api-in-one/relay"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/gin-gonic/gin"
)

func TestChatCompletionsConvertsToClaudeOnlyUpstream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var gotPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
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
	defer upstream.Close()

	ch := model.NewChannel("OpenModel", "openai", "", upstream.URL+"/v1", "", false, []string{"om-key"}, []string{"deepseek-v4-flash"}, nil, 10, 100)
	engine := relay.NewEngine(relay.NewPool([]*model.Channel{ch}))
	h := NewRelay(engine)
	r := gin.New()
	r.POST("/v1/chat/completions", h.ChatCompletions)

	body := `{"model":"deepseek-v4-flash","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected chat completion to succeed, got %d: %s", w.Code, w.Body.String())
	}
	if gotPath != "/v1/messages" {
		t.Fatalf("expected Claude upstream /v1/messages, got %q", gotPath)
	}
	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "ok" {
		t.Fatalf("unexpected chat response: %#v", resp)
	}
}

func TestChatCompletionsRoundRobinAcrossOpenAIAndClaudeOnlyChannels(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var openAICount atomic.Int32
	var claudeCount atomic.Int32

	openAIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openAICount.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("expected openai path /v1/chat/completions, got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.ChatCompletionResponse{
			ID:      "chatcmpl_openai",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "deepseek-v4-flash",
			Choices: []model.Choice{{
				Index: 0,
				Message: &model.Message{
					Role:    "assistant",
					Content: "from-openai",
				},
			}},
		})
	}))
	defer openAIUpstream.Close()

	claudeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claudeCount.Add(1)
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected claude path /v1/messages, got %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "deepseek-v4-flash",
			"content":       []map[string]interface{}{{"type": "text", "text": "from-claude"}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]interface{}{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer claudeUpstream.Close()

	openAIChannel := model.NewChannel("OpenAI", "openai", openAIUpstream.URL+"/v1", "", "", false, []string{"oa-key"}, []string{"model-openai"}, nil, 10, 100)
	claudeOnlyChannel := model.NewChannel("ClaudeOnly", "openai", "", claudeUpstream.URL+"/v1", "", false, []string{"cl-key"}, []string{"model-claude"}, nil, 10, 100)
	engine := relay.NewEngine(relay.NewPool([]*model.Channel{openAIChannel, claudeOnlyChannel}))
	h := NewRelay(engine)
	r := gin.New()
	r.POST("/v1/chat/completions", h.ChatCompletions)

	// Test 1: OpenAI native channel (should pass through without conversion)
	{
		body := `{"model":"model-openai","messages":[{"role":"user","content":"hello"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("openai request expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp model.ChatCompletionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("openai decode response: %v", err)
		}
		if len(resp.Choices) != 1 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "from-openai" {
			t.Fatalf("openai request expected from-openai, got %#v", resp)
		}
	}

	// Test 2: Claude only channel (should convert OpenAI request to Claude Messages)
	{
		body := `{"model":"model-claude","messages":[{"role":"user","content":"hello"}]}`
		req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("claude request expected 200, got %d: %s", w.Code, w.Body.String())
		}
		var resp model.ChatCompletionResponse
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("claude decode response: %v", err)
		}
		if len(resp.Choices) != 1 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "from-claude" {
			t.Fatalf("claude request expected from-claude, got %#v", resp)
		}
	}

	if openAICount.Load() != 1 {
		t.Fatalf("expected openai upstream to be called once, got %d", openAICount.Load())
	}
	if claudeCount.Load() != 1 {
		t.Fatalf("expected claude upstream to be called once, got %d", claudeCount.Load())
	}
}

func TestChatCompletionsRoundRobinHonorsModelMappingForClaudeOnlyChannel(t *testing.T) {
	gin.SetMode(gin.TestMode)
	var openAICount atomic.Int32
	var claudeCount atomic.Int32

	openAIUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		openAICount.Add(1)
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("expected openai path /v1/chat/completions, got %q", r.URL.Path)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode openai request: %v", err)
		}
		if req["model"] != "public-model" {
			t.Fatalf("expected openai upstream model public-model, got %#v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(model.ChatCompletionResponse{
			ID:      "chatcmpl_openai",
			Object:  "chat.completion",
			Created: 1700000000,
			Model:   "public-model",
			Choices: []model.Choice{{
				Index: 0,
				Message: &model.Message{
					Role:    "assistant",
					Content: "from-openai",
				},
			}},
		})
	}))
	defer openAIUpstream.Close()

	claudeUpstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		claudeCount.Add(1)
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("expected claude path /v1/messages, got %q", r.URL.Path)
		}
		var req map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode claude request: %v", err)
		}
		if req["model"] != "upstream-model" {
			t.Fatalf("expected claude upstream model upstream-model, got %#v", req["model"])
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":            "msg_test",
			"type":          "message",
			"role":          "assistant",
			"model":         "upstream-model",
			"content":       []map[string]interface{}{{"type": "text", "text": "from-claude"}},
			"stop_reason":   "end_turn",
			"stop_sequence": nil,
			"usage":         map[string]interface{}{"input_tokens": 1, "output_tokens": 1},
		})
	}))
	defer claudeUpstream.Close()

	claudeOnlyChannel := model.NewChannel("ClaudeMapped", "openai", "", claudeUpstream.URL+"/v1", "", false, []string{"cl-key"}, []string{"upstream-model"}, map[string]string{"public-model": "upstream-model"}, 10, 100)
	engine := relay.NewEngine(relay.NewPool([]*model.Channel{claudeOnlyChannel}))
	h := NewRelay(engine)
	r := gin.New()
	r.POST("/v1/chat/completions", h.ChatCompletions)

	body := `{"model":"public-model","messages":[{"role":"user","content":"hello"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("request expected 200, got %d: %s", w.Code, w.Body.String())
	}
	var resp model.ChatCompletionResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Choices) != 1 || resp.Choices[0].Message == nil || resp.Choices[0].Message.Content != "from-claude" {
		t.Fatalf("expected from-claude, got %#v", resp)
	}

	if openAICount.Load() != 0 {
		t.Fatalf("expected openai upstream to be called 0 times, got %d", openAICount.Load())
	}
	if claudeCount.Load() != 1 {
		t.Fatalf("expected claude upstream to be called once, got %d", claudeCount.Load())
	}
}
