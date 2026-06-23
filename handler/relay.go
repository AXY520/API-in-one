package handler

import (
	"api-in-one/config"
	"api-in-one/model"
	"api-in-one/relay"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Relay handles chat completion requests.
type Relay struct {
	engine *relay.Engine
}

// NewRelay creates a new Relay handler.
func NewRelay(engine *relay.Engine) *Relay {
	return &Relay{engine: engine}
}

// ChatCompletions handles POST /v1/chat/completions
func (h *Relay) ChatCompletions(c *gin.Context) {
	rawBody, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.Error{
				Message: "invalid request body: " + err.Error(),
				Type:    "invalid_request_error",
			},
		})
		return
	}
	var req model.ChatCompletionRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.Error{
				Message: "invalid request body: " + err.Error(),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	if req.Model == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.Error{
				Message: "model is required",
				Type:    "invalid_request_error",
				Code:    "missing_model",
			},
		})
		return
	}
	if !requestCanUseModel(c, req.Model) {
		c.JSON(http.StatusForbidden, model.ErrorResponse{
			Error: model.Error{
				Message: fmt.Sprintf("API key is not allowed to access model %q", req.Model),
				Type:    "permission_error",
				Param:   "model",
				Code:    "model_not_allowed",
			},
		})
		return
	}

	if len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.Error{
				Message: "messages array is required and must not be empty",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	rawBody = h.applyRawOpenAISystemPrompt(rawBody, &req)

	start := time.Now()
	logID := beginRequestLog(RequestLog{
		Protocol:  "openai",
		Model:     req.Model,
		Status:    102,
		Stream:    req.Stream,
		Request:   req,
		AccessKey: requestAccessKey(c),
	})
	result, rawResult, err := h.engine.DoOpenAIChat(c.Request.Context(), &req, rawBody, c.Request.Header)
	if err != nil {
		attempts := attemptsFromError(err)
		statusCode := errorStatusCode(err)
		mode := "converted"
		if last := lastAttempt(attempts); last != nil && last.ConversionMode != "" {
			mode = last.ConversionMode
		}
		slog.Error("relay failed", "model", req.Model, "error", err, "took", time.Since(start))
		finishRequestLog(logID, RequestLog{
			Protocol:  "openai",
			Mode:      mode,
			Model:     req.Model,
			Status:    statusCode,
			Duration:  time.Since(start).Milliseconds(),
			Stream:    req.Stream,
			Error:     err.Error(),
			Attempts:  attempts,
			Request:   req,
			AccessKey: requestAccessKey(c),
		})
		c.JSON(statusCode, model.ErrorResponse{
			Error: model.Error{
				Message: fmt.Sprintf("relay error: %v", err),
				Type:    "upstream_error",
			},
		})
		return
	}

	if rawResult != nil {
		slog.Info("relay success",
			"model", req.Model,
			"channel", rawResult.Channel,
			"resolved_model", rawResult.Model,
			"stream", req.Stream,
			"took", time.Since(start),
		)
		mode := rawResult.ConversionMode
		if mode == "" {
			mode = "passthrough"
		}
		status := 200
		if rawResult.Response != nil {
			status = rawResult.Response.StatusCode
		}
		finishRequestLog(logID, RequestLog{
			Protocol:      "openai",
			Mode:          mode,
			Model:         req.Model,
			ResolvedModel: rawResult.Model,
			Channel:       rawResult.Channel,
			Status:        status,
			Duration:      time.Since(start).Milliseconds(),
			Stream:        req.Stream,
			Attempts:      rawResult.Attempts,
			Request:       req,
			AccessKey:     requestAccessKey(c),
		})

		if rawResult.UpstreamProtocol == "responses" {
			h.writeResponsesAsChat(c, rawResult.Response, req.Stream, rawResult.Model)
			return
		}
		h.writeRawResponse(c, rawResult.Response)
		return
	}

	slog.Info("relay success",
		"model", req.Model,
		"channel", result.Channel,
		"resolved_model", result.Model,
		"stream", req.Stream,
		"took", time.Since(start),
	)
	mode := "converted"
	if last := lastAttempt(result.Attempts); last != nil && last.ConversionMode != "" {
		mode = last.ConversionMode
	}
	var promptTokens, completionTokens, totalTokens int
	if result.Response != nil {
		promptTokens = result.Response.Usage.PromptTokens
		completionTokens = result.Response.Usage.CompletionTokens
		totalTokens = result.Response.Usage.TotalTokens
	}
	finishRequestLog(logID, RequestLog{
		Protocol:         "openai",
		Mode:             mode,
		Model:            req.Model,
		ResolvedModel:    result.Model,
		Channel:          result.Channel,
		Status:           200,
		Duration:         time.Since(start).Milliseconds(),
		Stream:           req.Stream,
		Attempts:         result.Attempts,
		Request:          req,
		AccessKey:        requestAccessKey(c),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	})
	if req.Stream {
		h.writeOpenAIStream(c, result)
		return
	}
	c.JSON(http.StatusOK, result.Response)
}

func lastAttempt(attempts []relay.AttemptLog) *relay.AttemptLog {
	if len(attempts) == 0 {
		return nil
	}
	return &attempts[len(attempts)-1]
}

func (h *Relay) applyRawOpenAISystemPrompt(rawBody []byte, req *model.ChatCompletionRequest) []byte {
	ch, resolved, err := h.engine.PeekInboundRoute(req.Model, "openai")
	if err != nil || ch == nil {
		return rawBody
	}
	prompt := systemPromptForModel(req.Model, resolved)
	if prompt == "" {
		return rawBody
	}
	reqCopy := *req
	reqCopy.Messages = append([]model.Message(nil), req.Messages...)
	applyModelSystemPrompt(&reqCopy, prompt)
	data, err := json.Marshal(reqCopy)
	if err != nil {
		return rawBody
	}
	return data
}

func requestAccessKey(c *gin.Context) string {
	if v, ok := c.Get("api_key_masked"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func requestCanUseModel(c *gin.Context, modelName string) bool {
	if isAdmin, _ := c.Get("is_admin"); isAdmin == true {
		return true
	}
	value, ok := c.Get("access_key_config")
	if !ok {
		return true
	}
	accessKey, ok := value.(config.AccessKeyConfig)
	if !ok {
		return true
	}
	return config.AccessKeyCanUseModel(accessKey, modelName)
}

func attemptsFromError(err error) []relay.AttemptLog {
	var relayErr *relay.RelayError
	if errors.As(err, &relayErr) {
		return relayErr.Attempts
	}
	return nil
}

func errorStatusCode(err error) int {
	attempts := attemptsFromError(err)
	if len(attempts) > 0 {
		last := attempts[len(attempts)-1]
		if last.Status >= 400 && last.Status < 600 {
			return last.Status
		}
	}
	return http.StatusBadGateway
}


func (h *Relay) writeRawResponse(c *gin.Context, resp *http.Response) {
	defer resp.Body.Close()
	copyResponseHeaders(c, resp)
	c.Status(resp.StatusCode)
	if flusher, ok := c.Writer.(http.Flusher); ok {
		buf := make([]byte, 32*1024)
		for {
			n, err := resp.Body.Read(buf)
			if n > 0 {
				if _, writeErr := c.Writer.Write(buf[:n]); writeErr != nil {
					return
				}
				flusher.Flush()
			}
			if err == io.EOF {
				return
			}
			if err != nil {
				slog.Error("raw response read error", "error", err)
				return
			}
		}
	}
	io.Copy(c.Writer, resp.Body)
}

func (h *Relay) writeOpenAIStream(c *gin.Context, result *relay.RelayResult) {
	if result.SSE != nil {
		defer result.SSE.Close()
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	flusher, _ := c.Writer.(http.Flusher)
	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			fmt.Fprint(c.Writer, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if err != nil {
			return
		}
		if _, writeErr := c.Writer.Write(data); writeErr != nil {
			return
		}
		if flusher != nil {
			flusher.Flush()
		}
	}
}

func (h *Relay) writeResponsesAsChat(c *gin.Context, resp *http.Response, stream bool, modelName string) {
	defer resp.Body.Close()
	if stream {
		writeResponsesStreamAsChat(c, resp, modelName)
		return
	}
	var payload map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		c.JSON(http.StatusBadGateway, model.ErrorResponse{Error: model.Error{
			Message: "parse responses upstream: " + err.Error(),
			Type:    "upstream_error",
		}})
		return
	}
	c.JSON(resp.StatusCode, responsesPayloadToChatCompletion(payload, modelName))
}

func responsesPayloadToChatCompletion(payload map[string]interface{}, modelName string) model.ChatCompletionResponse {
	content, toolCalls := extractResponsesOutput(payload)
	finishReason := "stop"
	if len(toolCalls) > 0 {
		finishReason = "tool_calls"
	}
	created := time.Now().Unix()
	if rawCreated, ok := payload["created_at"].(float64); ok && rawCreated > 0 {
		created = int64(rawCreated)
	}
	id, _ := payload["id"].(string)
	if id == "" {
		id = fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli())
	}
	if rawModel, ok := payload["model"].(string); ok && rawModel != "" {
		modelName = rawModel
	}
	resp := model.ChatCompletionResponse{
		ID:      id,
		Object:  "chat.completion",
		Created: created,
		Model:   modelName,
		Choices: []model.Choice{{
			Index: 0,
			Message: &model.Message{
				Role:      "assistant",
				Content:   content,
				ToolCalls: toolCalls,
			},
			FinishReason: &finishReason,
		}},
	}
	if usage, ok := payload["usage"].(map[string]interface{}); ok {
		resp.Usage = model.Usage{
			PromptTokens:     intNumber(usage["input_tokens"]),
			CompletionTokens: intNumber(usage["output_tokens"]),
			TotalTokens:      intNumber(usage["total_tokens"]),
		}
	}
	return resp
}

func extractResponsesOutput(payload map[string]interface{}) (string, []model.ToolCall) {
	var text strings.Builder
	var toolCalls []model.ToolCall
	output, _ := payload["output"].([]interface{})
	for _, item := range output {
		m, ok := item.(map[string]interface{})
		if !ok {
			continue
		}
		switch m["type"] {
		case "message":
			if parts, ok := m["content"].([]interface{}); ok {
				for _, part := range parts {
					pm, ok := part.(map[string]interface{})
					if !ok {
						continue
					}
					if t, ok := pm["text"].(string); ok {
						text.WriteString(t)
					}
				}
			}
		case "function_call":
			fn := model.FunctionCall{}
			if name, ok := m["name"].(string); ok {
				fn.Name = name
			}
			if args, ok := m["arguments"].(string); ok {
				fn.Arguments = args
			}
			id, _ := m["call_id"].(string)
			if id == "" {
				id, _ = m["id"].(string)
			}
			toolCalls = append(toolCalls, model.ToolCall{
				Index:    len(toolCalls),
				ID:       id,
				Type:     "function",
				Function: fn,
			})
		}
	}
	if text.Len() == 0 {
		if outputText, ok := payload["output_text"].(string); ok {
			text.WriteString(outputText)
		}
	}
	return text.String(), toolCalls
}

func writeResponsesStreamAsChat(c *gin.Context, resp *http.Response, modelName string) {
	copyResponseHeaders(c, resp)
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(resp.StatusCode)
	flusher, _ := c.Writer.(http.Flusher)
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			fmt.Fprint(c.Writer, "data: [DONE]\n\n")
			if flusher != nil {
				flusher.Flush()
			}
			return
		}
		if chunk := responsesStreamEventToChatChunk([]byte(data), modelName); chunk != nil {
			b, _ := json.Marshal(chunk)
			fmt.Fprintf(c.Writer, "data: %s\n\n", string(b))
			if flusher != nil {
				flusher.Flush()
			}
		}
	}
	fmt.Fprint(c.Writer, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func responsesStreamEventToChatChunk(data []byte, modelName string) *model.ChatCompletionChunk {
	var event map[string]interface{}
	if err := json.Unmarshal(data, &event); err != nil {
		return nil
	}
	eventType, _ := event["type"].(string)
	var content string
	switch eventType {
	case "response.output_text.delta":
		content, _ = event["delta"].(string)
	case "response.completed":
		return &model.ChatCompletionChunk{
			ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli()),
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Model:   modelName,
			Choices: []model.ChunkChoice{{Index: 0, Delta: &model.Message{}, FinishReason: stringPtr("stop")}},
		}
	default:
		return nil
	}
	if content == "" {
		return nil
	}
	return &model.ChatCompletionChunk{
		ID:      fmt.Sprintf("chatcmpl-%d", time.Now().UnixMilli()),
		Object:  "chat.completion.chunk",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []model.ChunkChoice{{Index: 0, Delta: &model.Message{Content: content}}},
	}
}

func intNumber(value interface{}) int {
	switch v := value.(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func stringPtr(value string) *string {
	return &value
}

func copyResponseHeaders(c *gin.Context, resp *http.Response) {
	for key, values := range resp.Header {
		if shouldSkipResponseHeader(key) {
			continue
		}
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}
	c.Header("X-Accel-Buffering", "no")
}

func shouldSkipResponseHeader(key string) bool {
	switch http.CanonicalHeaderKey(key) {
	case "Connection", "Transfer-Encoding", "Keep-Alive", "Proxy-Authenticate", "Proxy-Authorization", "Trailer", "Upgrade":
		return true
	default:
		return false
	}
}
