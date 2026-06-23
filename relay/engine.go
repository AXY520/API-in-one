package relay

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
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

func (e *Engine) PeekInboundRoute(requestedModel string, protocol string) (*model.Channel, string, error) {
	return e.pool.PeekChannelForInboundProtocol(requestedModel, protocol)
}

// RelayResult holds the outcome of a relay attempt.
type RelayResult struct {
	Response          *model.ChatCompletionResponse
	SSE               adaptor.SSEProcessor
	Raw               *http.Response
	IsStream          bool
	Channel           string
	Model             string
	DisableMiMoCompat bool
	Attempts          []AttemptLog
}

// RawRelayResult holds a same-protocol passthrough response.
type RawRelayResult struct {
	Response         *http.Response
	IsStream         bool
	Channel          string
	Model            string
	Attempts         []AttemptLog
	InboundProtocol  string
	UpstreamProtocol string
	ConversionMode   string
}

type AttemptLog struct {
	Attempt          int    `json:"attempt"`
	Channel          string `json:"channel"`
	KeyIndex         int    `json:"key_index"`
	MaskedKey        string `json:"masked_key"`
	Model            string `json:"model"`
	Status           int    `json:"status"`
	DurationMs       int64  `json:"duration_ms"`
	Error            string `json:"error,omitempty"`
	Retryable        bool   `json:"retryable"`
	Protocol         string `json:"protocol"`
	InboundProtocol  string `json:"inbound_protocol,omitempty"`
	UpstreamProtocol string `json:"upstream_protocol,omitempty"`
	UpstreamURL      string `json:"upstream_url,omitempty"`
	ConversionMode   string `json:"conversion_mode,omitempty"`
	AdaptorName      string `json:"adaptor"`
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

func (e *Engine) DoRaw(ctx context.Context, protocol, requestedModel string, stream bool, rawBody []byte, inboundHeader http.Header) (*RawRelayResult, error) {
	var lastErr error
	var attempts []AttemptLog
	excludedChannels := make(map[string]bool)

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectChannelForInboundProtocolExcluding(requestedModel, protocol, excludedChannels)
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

		upstreamProtocol := protocol
		var body []byte
		if protocol == "openai" && ch.ResponsesOnly {
			upstreamProtocol = "responses"
			body, err = chatRawToResponsesRaw(rawBody, resolvedModel)
		} else {
			if requestedModel == resolvedModel {
				body = rawBody
			} else {
				body, err = replaceRawModel(rawBody, resolvedModel)
			}
		}
		if err != nil {
			return nil, err
		}
		req, err := buildRawHTTPRequest(ctx, ch, upstreamProtocol, key, body, inboundHeader)
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
		e.recordModelResult(ch, requestedModel, resolvedModel, status, attemptDuration, err)
		attemptLog := AttemptLog{
			Attempt:          attempt + 1,
			Channel:          ch.Name,
			KeyIndex:         ch.KeyIndex(key),
			MaskedKey:        maskKey(key),
			Model:            resolvedModel,
			Status:           status,
			DurationMs:       attemptDuration.Milliseconds(),
			Error:            errStr(err),
			Retryable:        isRetryableStatus(status),
			Protocol:         protocol,
			InboundProtocol:  protocol,
			UpstreamProtocol: upstreamProtocol,
			UpstreamURL:      req.URL.String(),
			ConversionMode:   conversionMode(protocol, upstreamProtocol),
			AdaptorName:      upstreamProtocol,
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
				excludedChannels[ch.Name] = true
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
			Response:         resp,
			IsStream:         stream,
			Channel:          ch.Name,
			Model:            resolvedModel,
			Attempts:         attempts,
			InboundProtocol:  protocol,
			UpstreamProtocol: upstreamProtocol,
			ConversionMode:   conversionMode(protocol, upstreamProtocol),
		}, nil
	}

	return nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func (e *Engine) DoOpenAIChat(ctx context.Context, req *model.ChatCompletionRequest, rawBody []byte, inboundHeader http.Header) (*RelayResult, *RawRelayResult, error) {
	var lastErr error
	var attempts []AttemptLog
	excludedChannels := make(map[string]bool)

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectAnyChannelExcluding(req.Model, excludedChannels)
		if err != nil {
			return nil, nil, err
		}
		upstreamProtocol := inferOpenAIInboundUpstreamProtocol(ch)
		key := ch.NextKeyForModel(resolvedModel)
		if key == "" {
			lastErr = fmt.Errorf("channel %s: no key available", ch.Name)
			attempts = append(attempts, AttemptLog{
				Attempt:          attempt + 1,
				Channel:          ch.Name,
				Model:            resolvedModel,
				Status:           0,
				Error:            lastErr.Error(),
				Protocol:         "openai",
				InboundProtocol:  "openai",
				UpstreamProtocol: upstreamProtocol,
				ConversionMode:   conversionMode("openai", upstreamProtocol),
			})
			continue
		}

		if upstreamProtocol == "openai" || upstreamProtocol == "responses" {
			var body []byte
			var err error
			if upstreamProtocol == "responses" {
				body, err = chatRawToResponsesRaw(rawBody, resolvedModel)
			} else {
				if req.Model == resolvedModel {
					body = rawBody
				} else {
					body, err = replaceRawModel(rawBody, resolvedModel)
				}
			}
			if err != nil {
				return nil, nil, err
			}
			httpReq, err := buildRawHTTPRequest(ctx, ch, upstreamProtocol, key, body, inboundHeader)
			if err != nil {
				return nil, nil, err
			}
			attemptStart := time.Now()
			resp, err := e.httpClient.Do(httpReq)
			attemptDuration := time.Since(attemptStart)
			status := 0
			if resp != nil {
				status = resp.StatusCode
			}
			ch.RecordKeyResult(key, status, attemptDuration, err)
			e.recordModelResult(ch, req.Model, resolvedModel, status, attemptDuration, err)
			attemptLog := AttemptLog{
				Attempt:          attempt + 1,
				Channel:          ch.Name,
				KeyIndex:         ch.KeyIndex(key),
				MaskedKey:        maskKey(key),
				Model:            resolvedModel,
				Status:           status,
				DurationMs:       attemptDuration.Milliseconds(),
				Error:            errStr(err),
				Retryable:        isRetryableStatus(status),
				Protocol:         "openai",
				InboundProtocol:  "openai",
				UpstreamProtocol: upstreamProtocol,
				UpstreamURL:      httpReq.URL.String(),
				ConversionMode:   conversionMode("openai", upstreamProtocol),
				AdaptorName:      upstreamProtocol,
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
					excludedChannels[ch.Name] = true
					select {
					case <-ctx.Done():
						return nil, nil, ctx.Err()
					case <-time.After(backoffDelay(attempt)):
					}
				}
				continue
			}
			ch.RecordSuccess()
			return nil, &RawRelayResult{
				Response:         resp,
				IsStream:         req.Stream,
				Channel:          ch.Name,
				Model:            resolvedModel,
				Attempts:         attempts,
				InboundProtocol:  "openai",
				UpstreamProtocol: upstreamProtocol,
				ConversionMode:   conversionMode("openai", upstreamProtocol),
			}, nil
		}

		result, attemptLog, retryable, err := e.executeConvertedAttempt(ctx, req, "openai", upstreamProtocol, ch, resolvedModel, attempt+1)
		attempts = append(attempts, attemptLog)
		if err != nil {
			lastErr = err
			if retryable && attempt < e.maxRetries-1 {
				excludedChannels[ch.Name] = true
				select {
				case <-ctx.Done():
					return nil, nil, ctx.Err()
				case <-time.After(backoffDelay(attempt)):
				}
			}
			continue
		}

		result.Attempts = attempts
		return result, nil, nil
	}

	return nil, nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func (e *Engine) DoConvertedAny(ctx context.Context, req *model.ChatCompletionRequest, inboundProtocol string) (*RelayResult, error) {
	var lastErr error
	var attempts []AttemptLog
	excludedChannels := make(map[string]bool)

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectAnyChannelForInboundExcluding(req.Model, inboundProtocol, excludedChannels)
		if err != nil {
			return nil, err
		}
		upstreamProtocol := inferOpenAIInboundUpstreamProtocol(ch)
		if upstreamProtocol == "responses" {
			upstreamProtocol = "openai"
		}
		result, attemptLog, retryable, err := e.executeConvertedAttempt(ctx, req, inboundProtocol, upstreamProtocol, ch, resolvedModel, attempt+1)
		attempts = append(attempts, attemptLog)
		if err != nil {
			lastErr = err
			if retryable && attempt < e.maxRetries-1 {
				excludedChannels[ch.Name] = true
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(backoffDelay(attempt)):
				}
			}
			continue
		}
		result.Attempts = attempts
		return result, nil
	}

	return nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func (e *Engine) executeConvertedAttempt(ctx context.Context, req *model.ChatCompletionRequest, inboundProtocol string, upstreamProtocol string, ch *model.Channel, resolvedModel string, attemptNumber int) (*RelayResult, AttemptLog, bool, error) {
	key := ch.NextKeyForModel(resolvedModel)
	if key == "" {
		err := fmt.Errorf("channel %s: no key available", ch.Name)
		return nil, AttemptLog{
			Attempt:          attemptNumber,
			Channel:          ch.Name,
			Model:            resolvedModel,
			Status:           0,
			Error:            err.Error(),
			Protocol:         inboundProtocol,
			InboundProtocol:  inboundProtocol,
			UpstreamProtocol: upstreamProtocol,
			ConversionMode:   conversionMode(inboundProtocol, upstreamProtocol),
		}, false, err
	}

	reqCopy := *req
	reqCopy.Model = resolvedModel
	if len(req.Messages) > 0 {
		reqCopy.Messages = append([]model.Message(nil), req.Messages...)
	}
	if len(req.Tools) > 0 {
		reqCopy.Tools = append([]model.Tool(nil), req.Tools...)
	}
	ad := e.pool.GetAdaptor(upstreamProtocol)
	if ad == nil {
		err := fmt.Errorf("no adaptor for type: %s", upstreamProtocol)
		return nil, AttemptLog{
			Attempt:          attemptNumber,
			Channel:          ch.Name,
			KeyIndex:         ch.KeyIndex(key),
			MaskedKey:        maskKey(key),
			Model:            resolvedModel,
			Status:           0,
			Error:            err.Error(),
			Protocol:         inboundProtocol,
			InboundProtocol:  inboundProtocol,
			UpstreamProtocol: upstreamProtocol,
			ConversionMode:   conversionMode(inboundProtocol, upstreamProtocol),
		}, false, err
	}

	preserveTools := ad.Name() == "openai" || ad.Name() == "claude"
	sanitizeRequest(&reqCopy, preserveTools)

	attemptStart := time.Now()
	result, status, upstreamURL, err := e.doRequest(ctx, ad, ch, key, &reqCopy, upstreamProtocol)
	attemptDuration := time.Since(attemptStart)
	ch.RecordKeyResult(key, status, attemptDuration, err)
	e.recordModelResult(ch, req.Model, resolvedModel, status, attemptDuration, err)
	attemptLog := AttemptLog{
		Attempt:          attemptNumber,
		Channel:          ch.Name,
		KeyIndex:         ch.KeyIndex(key),
		MaskedKey:        maskKey(key),
		Model:            resolvedModel,
		Status:           status,
		DurationMs:       attemptDuration.Milliseconds(),
		Error:            errStr(err),
		Retryable:        isRetryableStatus(status),
		Protocol:         inboundProtocol,
		InboundProtocol:  inboundProtocol,
		UpstreamProtocol: upstreamProtocol,
		UpstreamURL:      upstreamURL,
		ConversionMode:   conversionMode(inboundProtocol, upstreamProtocol),
		AdaptorName:      ad.Name(),
	}
	if err != nil {
		if status == 0 || status >= 500 {
			ch.RecordFailure()
		}
		return nil, attemptLog, isRetryableStatus(status), err
	}

	ch.RecordSuccess()
	result.Channel = ch.Name
	result.Model = resolvedModel
	result.DisableMiMoCompat = ch.DisableMiMoCompat
	return result, attemptLog, false, nil
}

func inferOpenAIInboundUpstreamProtocol(ch *model.Channel) string {
	if ch == nil {
		return "openai"
	}
	if ch.ResponsesOnly {
		return "responses"
	}
	if ch.SupportsProtocol("openai") {
		return "openai"
	}
	if ch.SupportsProtocol("claude") {
		return "claude"
	}
	if ch.SupportsProtocol("gemini") {
		return "gemini"
	}
	return ch.Type
}

func (e *Engine) DoRawResponses(ctx context.Context, requestedModel string, stream bool, rawBody []byte, inboundHeader http.Header) (*RawRelayResult, error) {
	var lastErr error
	var attempts []AttemptLog
	excludedChannels := make(map[string]bool)

	for attempt := 0; attempt < e.maxRetries; attempt++ {
		ch, resolvedModel, err := e.pool.SelectResponsesChannelExcluding(requestedModel, excludedChannels)
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

		var body []byte
		if requestedModel == resolvedModel {
			body = rawBody
		} else {
			body, err = replaceRawModel(rawBody, resolvedModel)
			if err != nil {
				return nil, err
			}
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
		e.recordModelResult(ch, requestedModel, resolvedModel, status, attemptDuration, err)
		attemptLog := AttemptLog{
			Attempt:          attempt + 1,
			Channel:          ch.Name,
			KeyIndex:         ch.KeyIndex(key),
			MaskedKey:        maskKey(key),
			Model:            resolvedModel,
			Status:           status,
			DurationMs:       attemptDuration.Milliseconds(),
			Error:            errStr(err),
			Retryable:        isRetryableStatus(status),
			Protocol:         "responses",
			InboundProtocol:  "responses",
			UpstreamProtocol: "responses",
			UpstreamURL:      req.URL.String(),
			ConversionMode:   "passthrough",
			AdaptorName:      "responses",
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
				excludedChannels[ch.Name] = true
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
			Response:         resp,
			IsStream:         stream,
			Channel:          ch.Name,
			Model:            resolvedModel,
			Attempts:         attempts,
			InboundProtocol:  "responses",
			UpstreamProtocol: "responses",
			ConversionMode:   "passthrough",
		}, nil
	}

	return nil, &RelayError{
		Err:      fmt.Errorf("%w: %v", ErrAllRetriesFailed, lastErr),
		Attempts: attempts,
	}
}

func (e *Engine) recordModelResult(ch *model.Channel, requestedModel, resolvedModel string, status int, latency time.Duration, err error) {
	threshold := config.Get().Server.ChannelModelFailureThreshold
	disabled := ch.RecordModelResult(requestedModel, resolvedModel, status, latency, err, threshold)
	if !disabled {
		return
	}
	if saveErr := config.UpdateChannelDisabledModels(ch.Name, ch.DisabledModelList()); saveErr != nil {
		slog.Warn("failed to persist disabled channel model", "channel", ch.Name, "model", requestedModel, "error", saveErr)
		return
	}
	slog.Warn("channel model temporarily disabled after consecutive failures", "channel", ch.Name, "model", requestedModel, "resolved_model", resolvedModel, "threshold", threshold)
}

func replaceRawModel(rawBody []byte, modelName string) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("parse raw request: %w", err)
	}
	body["model"] = modelName
	return json.Marshal(body)
}

func chatRawToResponsesRaw(rawBody []byte, modelName string) ([]byte, error) {
	var body map[string]interface{}
	if err := json.Unmarshal(rawBody, &body); err != nil {
		return nil, fmt.Errorf("parse raw request: %w", err)
	}
	out := map[string]interface{}{
		"model": modelName,
	}
	if stream, ok := body["stream"].(bool); ok {
		out["stream"] = stream
	}
	if maxTokens, ok := body["max_tokens"]; ok {
		out["max_output_tokens"] = maxTokens
	}
	for _, key := range []string{"temperature", "top_p", "tools", "tool_choice", "parallel_tool_calls"} {
		if value, ok := body[key]; ok {
			out[key] = value
		}
	}

	input := make([]interface{}, 0)
	if messages, ok := body["messages"].([]interface{}); ok {
		for _, item := range messages {
			msg, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			role, _ := msg["role"].(string)
			switch role {
			case "system":
				if out["instructions"] == nil {
					out["instructions"] = chatContentText(msg["content"])
				} else if text := chatContentText(msg["content"]); text != "" {
					out["instructions"] = fmt.Sprintf("%s\n%s", out["instructions"], text)
				}
			case "assistant":
				if calls, ok := msg["tool_calls"].([]interface{}); ok && len(calls) > 0 {
					for _, call := range calls {
						if converted := chatToolCallToResponsesItem(call); converted != nil {
							input = append(input, converted)
						}
					}
				} else {
					input = append(input, map[string]interface{}{
						"type":    "message",
						"role":    "assistant",
						"content": chatContentToResponsesContent(msg["content"], "output_text"),
					})
				}
			case "tool":
				input = append(input, map[string]interface{}{
					"type":    "function_call_output",
					"call_id": msg["tool_call_id"],
					"output":  chatContentText(msg["content"]),
				})
			default:
				if role == "" {
					role = "user"
				}
				input = append(input, map[string]interface{}{
					"type":    "message",
					"role":    role,
					"content": chatContentToResponsesContent(msg["content"], "input_text"),
				})
			}
		}
	}
	out["input"] = input
	return json.Marshal(out)
}

func chatToolCallToResponsesItem(call interface{}) map[string]interface{} {
	c, ok := call.(map[string]interface{})
	if !ok {
		return nil
	}
	fn, _ := c["function"].(map[string]interface{})
	return map[string]interface{}{
		"type":      "function_call",
		"call_id":   c["id"],
		"name":      fn["name"],
		"arguments": fn["arguments"],
	}
}

func chatContentText(content interface{}) string {
	switch v := content.(type) {
	case string:
		return v
	case []interface{}:
		var parts []string
		for _, item := range v {
			part, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			if text, ok := part["text"].(string); ok {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		if content == nil {
			return ""
		}
		return fmt.Sprintf("%v", content)
	}
}

func chatContentToResponsesContent(content interface{}, textType string) []map[string]interface{} {
	if textType == "" {
		textType = "input_text"
	}
	switch v := content.(type) {
	case []interface{}:
		parts := make([]map[string]interface{}, 0, len(v))
		for _, item := range v {
			part, ok := item.(map[string]interface{})
			if !ok {
				continue
			}
			switch part["type"] {
			case "text":
				parts = append(parts, map[string]interface{}{
					"type": textType,
					"text": part["text"],
				})
			case "image_url":
				parts = append(parts, map[string]interface{}{
					"type":      "input_image",
					"image_url": part["image_url"],
				})
			}
		}
		if len(parts) > 0 {
			return parts
		}
	}
	return []map[string]interface{}{{"type": textType, "text": chatContentText(content)}}
}

func buildRawHTTPRequest(ctx context.Context, ch *model.Channel, protocol, key string, body []byte, inboundHeader http.Header) (*http.Request, error) {
	baseURL := ch.GetBaseURL(protocol)
	var url string
	switch protocol {
	case "claude":
		url = adaptor.BuildClaudeURL(baseURL)
	case "responses":
		url = adaptor.BuildResponsesURL(baseURL)
	default:
		url = adaptor.BuildOpenAIChatCompletionsURL(baseURL)
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

func conversionMode(inboundProtocol string, upstreamProtocol string) string {
	if inboundProtocol != "" && upstreamProtocol != "" && inboundProtocol != upstreamProtocol {
		return "converted"
	}
	return "passthrough"
}

func (e *Engine) doRequest(ctx context.Context, ad adaptor.Adaptor, ch *model.Channel, key string, req *model.ChatCompletionRequest, protocol string) (*RelayResult, int, string, error) {
	baseURL := ch.GetBaseURL(protocol)
	httpReq, err := ad.BuildHTTPRequest(baseURL, key, req)
	if err != nil {
		return nil, 0, "", fmt.Errorf("build request: %w", err)
	}
	httpReq = httpReq.WithContext(ctx)
	upstreamURL := httpReq.URL.String()

	resp, err := e.httpClient.Do(httpReq)
	if err != nil {
		return nil, 0, upstreamURL, fmt.Errorf("http request: %w", err)
	}

	status := resp.StatusCode

	if req.Stream {
		if status != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, status, upstreamURL, fmt.Errorf("upstream stream error (status %d): %s", status, string(body))
		}
		sse := ad.StreamHandler(resp)
		if sse == nil {
			resp.Body.Close()
			return nil, status, upstreamURL, fmt.Errorf("streaming not supported for adaptor %s", ad.Name())
		}
		return &RelayResult{SSE: sse, IsStream: true}, status, upstreamURL, nil
	}

	if status != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, status, upstreamURL, fmt.Errorf("upstream error (status %d): %s", status, string(body))
	}
	chatResp, err := ad.ParseResponse(resp)
	if err != nil {
		return nil, status, upstreamURL, fmt.Errorf("parse response: %w", err)
	}
	return &RelayResult{Response: chatResp}, status, upstreamURL, nil
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

// backoffDelay returns exponential backoff with random jitter.
// Attempt 0: ~500ms, Attempt 1: ~1s, Attempt 2: ~2s (capped at 8s)
func backoffDelay(attempt int) time.Duration {
	base := 500 * time.Millisecond
	delay := base * time.Duration(1<<uint(attempt))
	if delay > 8*time.Second {
		delay = 8 * time.Second
	}
	jitter := time.Duration(rand.Int63n(251)) * time.Millisecond
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
