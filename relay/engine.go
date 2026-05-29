package relay

import (
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

var (
	ErrNoAvailableChannel = errors.New("no available channel for model")
	ErrAllRetriesFailed   = errors.New("all retry attempts failed")
)

// Engine orchestrates request routing, retries, and protocol conversion.
type Engine struct {
	pool       *Pool
	maxRetries int
	httpClient *http.Client
}

// NewEngine creates a new relay engine.
func NewEngine(pool *Pool) *Engine {
	return &Engine{
		pool:       pool,
		maxRetries: 3,
		httpClient: &http.Client{
			Timeout: 120 * time.Second,
		},
	}
}

// RelayResult holds the outcome of a relay attempt.
type RelayResult struct {
	Response *model.ChatCompletionResponse
	SSE      adaptor.SSEProcessor
	IsStream bool
	Channel  string
	Model    string
}

// Do executes a chat completion request with automatic retry and failover.
// protocol indicates the inbound protocol ("openai", "claude", "gemini", "responses")
// and is used to select the appropriate base URL when a channel has multiple endpoints.
func (e *Engine) Do(ctx context.Context, req *model.ChatCompletionRequest, protocol string) (*RelayResult, error) {
	// Sanitize messages and tools before sending upstream
	sanitizeRequest(req)

	var lastErr error

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectChannel(req.Model)
		if err != nil {
			return nil, err
		}

		reqCopy := *req
		reqCopy.Model = resolvedModel

		key := ch.NextKey()
		if key == "" {
			lastErr = fmt.Errorf("channel %s: no key available", ch.Name)
			continue
		}

		// Use the adaptor matching the inbound protocol, not the channel type.
		// This allows a single channel to serve multiple protocols via different URLs.
		adaptorType := protocol
		if adaptorType == "responses" {
			adaptorType = "openai" // Responses API uses OpenAI adaptor upstream
		}
		ad := e.pool.GetAdaptor(adaptorType)
		if ad == nil {
			ad = e.pool.GetAdaptor(ch.Type) // fallback to channel type
		}
		if ad == nil {
			lastErr = fmt.Errorf("no adaptor for type: %s or %s", adaptorType, ch.Type)
			continue
		}

		slog.Debug("relay attempt",
			"attempt", attempt+1,
			"channel", ch.Name,
			"model", resolvedModel,
			"stream", req.Stream,
		)

		result, status, err := e.doRequest(ctx, ad, ch, key, &reqCopy, protocol)
		if err != nil {
			if status == 0 || status >= 500 {
				ch.RecordFailure()
			}
			slog.Warn("relay failed",
				"channel", ch.Name,
				"status", status,
				"error", err,
			)
			lastErr = err
			// Retry with backoff on transient errors (rate limit / gateway)
			if isRetryableStatus(status) && attempt < e.maxRetries-1 {
				delay := backoffDelay(attempt)
				slog.Info("retrying with backoff", "delay", delay, "attempt", attempt+1)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}

		ch.RecordSuccess()
		result.Channel = ch.Name
		result.Model = resolvedModel
		return result, nil
	}

	return nil, fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr)
}

func (e *Engine) doRequest(ctx context.Context, ad adaptor.Adaptor, ch *model.Channel, key string, req *model.ChatCompletionRequest, protocol string) (*RelayResult, int, error) {
	baseURL := ch.GetBaseURL(protocol)
	httpReq, err := ad.BuildHTTPRequest(baseURL, key, req)
	if err != nil {
		return nil, 0, fmt.Errorf("build request: %w", err)
	}
	httpReq = httpReq.WithContext(ctx)

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, fmt.Errorf("http request: %w", err)
	}

	status := resp.StatusCode

	if req.Stream {
		if status != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, status, fmt.Errorf("upstream stream error (status %d): %s", status, string(body))
		}
		sse := ad.StreamHandler(resp)
		if sse == nil {
			resp.Body.Close()
			return nil, status, fmt.Errorf("streaming not supported for adaptor %s", ad.Name())
		}
		return &RelayResult{SSE: sse, IsStream: true}, status, nil
	}

	if status != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, status, fmt.Errorf("upstream error (status %d): %s", status, string(body))
	}
	chatResp, err := ad.ParseResponse(resp)
	if err != nil {
		return nil, status, fmt.Errorf("parse response: %w", err)
	}
	return &RelayResult{Response: chatResp}, status, nil
}

// sanitizeRequest cleans up messages and tool_calls for upstream compatibility.
func sanitizeRequest(req *model.ChatCompletionRequest) {
	// Keep tools/tool_choice — let the model decide whether to use them.
	// Salvage malformed tool_call arguments instead of stripping tools.
	sanitizeMessages(req)
	sanitizeToolCalls(req)
}

func sanitizeMessages(req *model.ChatCompletionRequest) {
	// Phase 1: Convert tool/function role messages to user messages
	var phase1 []model.Message
	for _, msg := range req.Messages {
		switch msg.Role {
		case "system", "developer":
			// developer role is Codex's equivalent of system
			msg.Role = "system"
			phase1 = append(phase1, msg)
		case "user", "assistant":
			phase1 = append(phase1, msg)
		case "tool":
			content := extractStringContent(msg.Content)
			if content != "" {
				if msg.ToolCallID != "" {
					phase1 = append(phase1, model.Message{
						Role:    "user",
						Content: fmt.Sprintf("[Tool Result for %s]\n%s", msg.ToolCallID, content),
					})
				} else {
					phase1 = append(phase1, model.Message{
						Role:    "user",
						Content: "[Tool Result]\n" + content,
					})
				}
			}
		case "function":
			content := extractStringContent(msg.Content)
			if content != "" {
				phase1 = append(phase1, model.Message{
					Role:    "user",
					Content: "[Function Result]\n" + content,
				})
			}
		default:
			slog.Debug("skipping unsupported message role", "role", msg.Role)
		}
	}

	// Phase 2: Strip tool_calls but preserve reasoning_content
	var phase2 []model.Message
	for _, msg := range phase1 {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			msg.ToolCalls = nil
			if isMessageEmpty(msg) && msg.ReasoningContent == "" {
				continue
			}
		}
		phase2 = append(phase2, msg)
	}

	if len(phase2) > 0 {
		req.Messages = phase2
	}
}

// sanitizeToolCalls validates tool_calls in assistant messages.
// Salvages malformed JSON arguments to "{}" to prevent downstream failures.
func sanitizeToolCalls(req *model.ChatCompletionRequest) {
	for i := range req.Messages {
		msg := &req.Messages[i]
		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}
		for j := range msg.ToolCalls {
			tc := &msg.ToolCalls[j]
			tc.Function.Arguments = salvageJSON(tc.Function.Arguments, tc.Function.Name)
		}
	}
}

// salvageJSON validates that the string is valid JSON.
// Returns "{}" if parsing fails, to prevent downstream 400 errors.
func salvageJSON(raw string, toolName string) string {
	if raw == "" {
		return "{}"
	}
	var tmp interface{}
	if err := json.Unmarshal([]byte(raw), &tmp); err != nil {
		slog.Warn("salvaging malformed tool_call arguments",
			"tool", toolName,
			"len", len(raw),
			"preview", truncate(raw, 80),
		)
		return "{}"
	}
	return raw
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

// isRetryableStatus returns true for rate limits and transient gateway errors.
var retryableStatuses = map[int]bool{
	429: true, // Too Many Requests
	500: true, // Internal Server Error
	502: true, // Bad Gateway
	503: true, // Service Unavailable
	504: true, // Gateway Timeout
}

func isRetryableStatus(status int) bool {
	return retryableStatuses[status]
}

// backoffDelay returns exponential backoff with jitter.
// Attempt 0: ~500ms, Attempt 1: ~1s, Attempt 2: ~2s (capped at 8s)
func backoffDelay(attempt int) time.Duration {
	base := 500 * time.Millisecond
	delay := base * time.Duration(1<<uint(attempt))
	if delay > 8*time.Second {
		delay = 8 * time.Second
	}
	// Add jitter: 0-250ms
	jitter := time.Duration(250) * time.Millisecond
	return delay + jitter
}

func isMessageEmpty(msg model.Message) bool {
	if msg.Content == nil {
		return true
	}
	if s, ok := msg.Content.(string); ok && s == "" {
		return true
	}
	return false
}

func extractStringContent(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, p := range v {
			if m, ok := p.(map[string]interface{}); ok {
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
