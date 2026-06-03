package relay

import (
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"bytes"
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
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.MaxIdleConns = 1000
	tr.MaxIdleConnsPerHost = 100
	tr.IdleConnTimeout = 90 * time.Second

	return &Engine{
		pool:       pool,
		maxRetries: 3,
		httpClient: &http.Client{
			Timeout:   120 * time.Second,
			Transport: tr,
		},
	}
}

func (e *Engine) PeekRoute(requestedModel string) (*model.Channel, string, error) {
	return e.pool.PeekChannel(requestedModel)
}

// RelayResult holds the outcome of a relay attempt.
type RelayResult struct {
	Response *model.ChatCompletionResponse
	SSE      adaptor.SSEProcessor
	Raw      *http.Response
	IsStream bool
	Channel  string
	Model    string
	Attempts []AttemptLog
}

// RawRelayResult holds a same-protocol passthrough response.
type RawRelayResult struct {
	Response *http.Response
	IsStream bool
	Channel  string
	Model    string
	Attempts []AttemptLog
}

type AttemptLog struct {
	Attempt     int    `json:"attempt"`
	Channel     string `json:"channel"`
	KeyIndex    int    `json:"key_index"`
	MaskedKey   string `json:"masked_key"`
	Model       string `json:"model"`
	Status      int    `json:"status"`
	DurationMs  int64  `json:"duration_ms"`
	Error       string `json:"error,omitempty"`
	Retryable   bool   `json:"retryable"`
	Protocol    string `json:"protocol"`
	AdaptorName string `json:"adaptor"`
}

type RelayError struct {
	Err      error
	Attempts []AttemptLog
}

func (e *RelayError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *RelayError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// Do executes a chat completion request with automatic retry and failover.
// protocol indicates the inbound protocol ("openai", "claude", "gemini", "responses")
// and is used to select the appropriate base URL when a channel has multiple endpoints.
func (e *Engine) Do(ctx context.Context, req *model.ChatCompletionRequest, protocol string) (*RelayResult, error) {
	return e.doChat(ctx, req, protocol, protocol, "")
}

// DoConverted executes a request that has already been converted to OpenAI chat completions.
func (e *Engine) DoConverted(ctx context.Context, req *model.ChatCompletionRequest, inboundProtocol string) (*RelayResult, error) {
	return e.doChat(ctx, req, inboundProtocol, "openai", "openai")
}

func (e *Engine) doChat(ctx context.Context, req *model.ChatCompletionRequest, protocol string, upstreamProtocol string, selectionProtocol string) (*RelayResult, error) {
	var lastErr error
	var attempts []AttemptLog

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		var ch *model.Channel
		var resolvedModel string
		var err error
		if selectionProtocol != "" {
			ch, resolvedModel, err = e.pool.SelectChannelForProtocol(req.Model, selectionProtocol)
		} else {
			ch, resolvedModel, err = e.pool.SelectChannel(req.Model)
		}
		if err != nil {
			return nil, err
		}

		reqCopy := *req
		reqCopy.Model = resolvedModel
		if len(req.Messages) > 0 {
			reqCopy.Messages = append([]model.Message(nil), req.Messages...)
		}
		if len(req.Tools) > 0 {
			reqCopy.Tools = append([]model.Tool(nil), req.Tools...)
		}

		key := ch.NextKeyForModel(resolvedModel)
		if key == "" {
			lastErr = fmt.Errorf("channel %s: no key available", ch.Name)
			attempts = append(attempts, AttemptLog{
				Attempt:  attempt + 1,
				Channel:  ch.Name,
				Model:    resolvedModel,
				Status:   0,
				Error:    lastErr.Error(),
				Protocol: protocol,
			})
			continue
		}

		adaptorType := upstreamProtocol
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

		preserveTools := ad.Name() == "openai" || ad.Name() == "claude"
		sanitizeRequest(&reqCopy, preserveTools)

		slog.Debug("relay attempt",
			"attempt", attempt+1,
			"channel", ch.Name,
			"model", resolvedModel,
			"stream", req.Stream,
		)

		attemptStart := time.Now()
		result, status, err := e.doRequest(ctx, ad, ch, key, &reqCopy, adaptorType)
		attemptDuration := time.Since(attemptStart)
		ch.RecordKeyResult(key, status, attemptDuration, err)
		attemptLog := AttemptLog{
			Attempt:     attempt + 1,
			Channel:     ch.Name,
			KeyIndex:    ch.KeyIndex(key),
			MaskedKey:   maskKey(key),
			Model:       resolvedModel,
			Status:      status,
			DurationMs:  attemptDuration.Milliseconds(),
			Error:       errStr(err),
			Retryable:   isRetryableStatus(status),
			Protocol:    protocol,
			AdaptorName: ad.Name(),
		}
		attempts = append(attempts, attemptLog)
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
		result.Attempts = attempts
		return result, nil
	}

	return nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func (e *Engine) DoRaw(ctx context.Context, protocol, requestedModel string, stream bool, rawBody []byte, inboundHeader http.Header) (*RawRelayResult, error) {
	var lastErr error
	var attempts []AttemptLog

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectChannelForProtocol(requestedModel, protocol)
		if err != nil {
			return nil, err
		}
		key := ch.NextKeyForModel(resolvedModel)
		if key == "" {
			lastErr = fmt.Errorf("channel %s: no key available", ch.Name)
			attempts = append(attempts, AttemptLog{
				Attempt:  attempt + 1,
				Channel:  ch.Name,
				Model:    resolvedModel,
				Status:   0,
				Error:    lastErr.Error(),
				Protocol: protocol,
			})
			continue
		}

		body, err := replaceRawModel(rawBody, resolvedModel)
		if err != nil {
			return nil, err
		}
		req, err := buildRawHTTPRequest(ctx, ch, protocol, key, body, inboundHeader)
		if err != nil {
			return nil, err
		}

		attemptStart := time.Now()
		resp, err := e.httpClient.Do(req)
		attemptDuration := time.Since(attemptStart)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		ch.RecordKeyResult(key, status, attemptDuration, err)
		attemptLog := AttemptLog{
			Attempt:     attempt + 1,
			Channel:     ch.Name,
			KeyIndex:    ch.KeyIndex(key),
			MaskedKey:   maskKey(key),
			Model:       resolvedModel,
			Status:      status,
			DurationMs:  attemptDuration.Milliseconds(),
			Error:       errStr(err),
			Retryable:   isRetryableStatus(status),
			Protocol:    protocol,
			AdaptorName: protocol,
		}
		attempts = append(attempts, attemptLog)
		if err != nil || status < 200 || status >= 400 {
			if resp != nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if err == nil {
					err = fmt.Errorf("upstream error (status %d): %s", status, string(body))
				}
			}
			if status == 0 || status >= 500 {
				ch.RecordFailure()
			}
			lastErr = err
			if isRetryableStatus(status) && attempt < e.maxRetries-1 {
				delay := backoffDelay(attempt)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}

		ch.RecordSuccess()
		return &RawRelayResult{
			Response: resp,
			IsStream: stream,
			Channel:  ch.Name,
			Model:    resolvedModel,
			Attempts: attempts,
		}, nil
	}

	return nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func (e *Engine) DoRawResponses(ctx context.Context, requestedModel string, stream bool, rawBody []byte, inboundHeader http.Header) (*RawRelayResult, error) {
	var lastErr error
	var attempts []AttemptLog

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectResponsesChannel(requestedModel)
		if err != nil {
			return nil, err
		}
		key := ch.NextKeyForModel(resolvedModel)
		if key == "" {
			lastErr = fmt.Errorf("channel %s: no key available", ch.Name)
			attempts = append(attempts, AttemptLog{
				Attempt:  attempt + 1,
				Channel:  ch.Name,
				Model:    resolvedModel,
				Status:   0,
				Error:    lastErr.Error(),
				Protocol: "responses",
			})
			continue
		}

		body, err := replaceRawModel(rawBody, resolvedModel)
		if err != nil {
			return nil, err
		}
		req, err := buildRawHTTPRequest(ctx, ch, "responses", key, body, inboundHeader)
		if err != nil {
			return nil, err
		}

		attemptStart := time.Now()
		resp, err := e.httpClient.Do(req)
		attemptDuration := time.Since(attemptStart)
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		ch.RecordKeyResult(key, status, attemptDuration, err)
		attemptLog := AttemptLog{
			Attempt:     attempt + 1,
			Channel:     ch.Name,
			KeyIndex:    ch.KeyIndex(key),
			MaskedKey:   maskKey(key),
			Model:       resolvedModel,
			Status:      status,
			DurationMs:  attemptDuration.Milliseconds(),
			Error:       errStr(err),
			Retryable:   isRetryableStatus(status),
			Protocol:    "responses",
			AdaptorName: "responses",
		}
		attempts = append(attempts, attemptLog)
		if err != nil || status < 200 || status >= 400 {
			if resp != nil {
				body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
				resp.Body.Close()
				if err == nil {
					err = fmt.Errorf("upstream error (status %d): %s", status, string(body))
				}
			}
			if status == 0 || status >= 500 {
				ch.RecordFailure()
			}
			lastErr = err
			if isRetryableStatus(status) && attempt < e.maxRetries-1 {
				delay := backoffDelay(attempt)
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(delay):
				}
			}
			continue
		}

		ch.RecordSuccess()
		return &RawRelayResult{
			Response: resp,
			IsStream: stream,
			Channel:  ch.Name,
			Model:    resolvedModel,
			Attempts: attempts,
		}, nil
	}

	return nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func replaceRawModel(rawBody []byte, modelName string) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("parse raw request: %w", err)
	}
	body["model"] = modelName
	return json.Marshal(body)
}

func buildRawHTTPRequest(ctx context.Context, ch *model.Channel, protocol, key string, body []byte, inboundHeader http.Header) (*http.Request, error) {
	baseURL := ch.GetBaseURL(protocol)
	var url string
	switch protocol {
	case "claude":
		url = buildRawClaudeURL(baseURL)
	case "responses":
		url = buildRawResponsesURL(baseURL)
	default:
		url = buildRawChatCompletionsURL(baseURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	userAgent := strings.TrimSpace(inboundHeader.Get("User-Agent"))
	if userAgent == "" {
		userAgent = adaptor.UpstreamUserAgent
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", inboundHeader.Get("Accept"))
	switch protocol {
	case "claude":
		req.Header.Set("x-api-key", key)
		version := inboundHeader.Get("anthropic-version")
		if version == "" {
			version = "2023-06-01"
		}
		req.Header.Set("anthropic-version", version)
		if beta := inboundHeader.Get("anthropic-beta"); beta != "" {
			req.Header.Set("anthropic-beta", beta)
		}
	default:
		req.Header.Set("Authorization", "Bearer "+key)
	}
	return req, nil
}

func buildRawResponsesURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/v1/responses") || strings.HasSuffix(baseURL, "/responses") {
		return baseURL
	}
	return baseURL + "/responses"
}

func buildRawChatCompletionsURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/chat/completions") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/chat/completions"
	}
	return baseURL + "/v1/chat/completions"
}

func buildRawClaudeURL(baseURL string) string {
	baseURL = strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(baseURL, "/messages") {
		return baseURL
	}
	if strings.HasSuffix(baseURL, "/v1") {
		return baseURL + "/messages"
	}
	return baseURL + "/v1/messages"
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

// sanitizeRequest cleans up messages for upstream compatibility.
func sanitizeRequest(req *model.ChatCompletionRequest, preserveTools bool) {
	sanitizeMessages(req, preserveTools)
	sanitizeToolCalls(req)
}

func sanitizeMessages(req *model.ChatCompletionRequest, preserveTools bool) {
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
			if preserveTools {
				phase1 = append(phase1, msg)
				continue
			}
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
			if preserveTools {
				msg.Role = "tool"
				phase1 = append(phase1, msg)
				continue
			}
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

	// Phase 2: Strip tool_calls only for upstreams that do not accept OpenAI tool messages.
	var phase2 []model.Message
	for _, msg := range phase1 {
		if !preserveTools && msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
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

func errStr(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func maskKey(key string) string {
	if len(key) <= 8 {
		return "****"
	}
	return key[:4] + "****" + key[len(key)-4:]
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
