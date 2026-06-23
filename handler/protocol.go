package handler

import (
	"api-in-one/relay"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// Protocol handles inbound requests in Claude and Gemini formats.
type Protocol struct {
	engine *relay.Engine
}

func NewProtocol(engine *relay.Engine) *Protocol {
	return &Protocol{engine: engine}
}

// ==================== Claude Inbound ====================

// ClaudeMessages handles POST /v1/messages (Claude format inbound)
func (h *Protocol) ClaudeMessages(c *gin.Context) {
	rawBody, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + readErr.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	var claudeReq claudeInboundRequest
	if err := json.Unmarshal(rawBody, &claudeReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	if !requestCanUseModel(c, claudeReq.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("API key is not allowed to access model %q", claudeReq.Model),
			"type":    "permission_error",
			"param":   "model",
			"code":    "model_not_allowed",
		}})
		return
	}
	rawBody = h.applyRawClaudeSystemPrompt(rawBody, &claudeReq)

	logID := beginRequestLog(RequestLog{
		Protocol:  "claude-inbound",
		Model:     claudeReq.Model,
		Status:    102,
		Stream:    claudeReq.Stream,
		Request:   claudeReq,
		AccessKey: requestAccessKey(c),
	})
	if rawResult, ok := h.tryClaudePassthrough(c, &claudeReq, rawBody, logID); ok {
		relayHandler := Relay{engine: h.engine}
		relayHandler.writeRawResponse(c, rawResult.Response)
		return
	}

	// Convert Claude → OpenAI
	oaiReq := claudeToOpenAI(&claudeReq)
	applyModelSystemPrompt(oaiReq, h.modelSystemPrompt(claudeReq.Model))

	start := time.Now()
	result, err := h.engine.DoConvertedAny(c.Request.Context(), oaiReq, "claude")
	if err != nil {
		statusCode := errorStatusCode(err)
		c.JSON(statusCode, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
			"type":    "upstream_error",
		}})
		finishRequestLog(logID, RequestLog{
			Protocol:        "claude-inbound",
			Model:           claudeReq.Model,
			Status:          statusCode,
			Duration:        time.Since(start).Milliseconds(),
			Stream:          claudeReq.Stream,
			Error:           err.Error(),
			Attempts:        attemptsFromError(err),
			Request:         claudeReq,
			UpstreamRequest: oaiReq,
			AccessKey:       requestAccessKey(c),
		})
		return
	}

	var promptTokens, completionTokens, totalTokens int
	if result.Response != nil {
		promptTokens = result.Response.Usage.PromptTokens
		completionTokens = result.Response.Usage.CompletionTokens
		totalTokens = result.Response.Usage.TotalTokens
	}
	finishRequestLog(logID, RequestLog{
		Protocol:         "claude-inbound",
		Model:            claudeReq.Model,
		ResolvedModel:    result.Model,
		Channel:          result.Channel,
		Status:           200,
		Duration:         time.Since(start).Milliseconds(),
		Stream:           claudeReq.Stream,
		Attempts:         result.Attempts,
		Request:          claudeReq,
		UpstreamRequest:  oaiReq,
		AccessKey:        requestAccessKey(c),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	})

	// MiMo sometimes emits tool calls as XML-like text. Keep this compatibility
	// scoped to MiMo/Xiaomi routes so normal model conversions stay standard.
	if result.Response != nil && !result.DisableMiMoCompat && isMiMoCompatResult(result.Channel, claudeReq.Model, result.Model) {
		cleanResponseToolCalls(result.Response)
	}

	if claudeReq.Stream {
		h.handleClaudeStream(c, result)
		return
	}

	// Convert OpenAI response → Claude response
	claudeResp := openAIToClaude(result.Response)
	c.JSON(http.StatusOK, claudeResp)
}

func (h *Protocol) tryClaudePassthrough(c *gin.Context, claudeReq *claudeInboundRequest, rawBody []byte, logID int64) (*relay.RawRelayResult, bool) {
	start := time.Now()
	result, err := h.engine.DoRaw(c.Request.Context(), "claude", claudeReq.Model, claudeReq.Stream, rawBody, c.Request.Header)
	if err != nil {
		if errors.Is(err, relay.ErrNoAvailableChannel) || shouldFallbackFromClaudePassthrough(err) {
			return nil, false
		}
		statusCode := errorStatusCode(err)
		c.JSON(statusCode, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
			"type":    "upstream_error",
		}})
		finishRequestLog(logID, RequestLog{
			Protocol:  "claude-inbound",
			Model:     claudeReq.Model,
			Status:    statusCode,
			Duration:  time.Since(start).Milliseconds(),
			Stream:    claudeReq.Stream,
			Error:     err.Error(),
			Attempts:  attemptsFromError(err),
			Request:   claudeReq,
			AccessKey: requestAccessKey(c),
		})
		return nil, true
	}
	finishRequestLog(logID, RequestLog{
		Protocol:      "claude-inbound",
		Model:         claudeReq.Model,
		ResolvedModel: result.Model,
		Channel:       result.Channel,
		Status:        result.Response.StatusCode,
		Duration:      time.Since(start).Milliseconds(),
		Stream:        claudeReq.Stream,
		Attempts:      result.Attempts,
		Request:       claudeReq,
		AccessKey:     requestAccessKey(c),
	})
	return result, true
}

func shouldFallbackFromClaudePassthrough(err error) bool {
	attempts := attemptsFromError(err)
	if len(attempts) == 0 {
		return false
	}
	for _, attempt := range attempts {
		if attempt.Status != http.StatusNotFound && attempt.Status != http.StatusMethodNotAllowed {
			return false
		}
	}
	return true
}

// ==================== Gemini Inbound ====================

// GeminiGenerate handles POST /v1beta/models/:model:generateContent (Gemini format inbound)
func (h *Protocol) GeminiGenerate(c *gin.Context) {
	modelName := c.Param("model")
	// Strip ":generateContent" or ":streamGenerateContent" suffix
	modelName = strings.TrimSuffix(modelName, ":generateContent")
	modelName = strings.TrimSuffix(modelName, ":streamGenerateContent")
	if !requestCanUseModel(c, modelName) {
		c.JSON(http.StatusForbidden, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("API key is not allowed to access model %q", modelName),
			"type":    "permission_error",
			"param":   "model",
			"code":    "model_not_allowed",
		}})
		return
	}

	var geminiReq geminiInboundRequest
	if err := c.ShouldBindJSON(&geminiReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}

	oaiReq := geminiToOpenAI(&geminiReq, modelName)
	applyModelSystemPrompt(oaiReq, h.modelSystemPrompt(modelName))

	start := time.Now()
	isStream := strings.Contains(c.Request.URL.Path, "streamGenerateContent")
	logID := beginRequestLog(RequestLog{
		Protocol:        "gemini-inbound",
		Model:           modelName,
		Status:          102,
		Stream:          isStream,
		Request:         geminiReq,
		UpstreamRequest: oaiReq,
		AccessKey:       requestAccessKey(c),
	})
	result, err := h.engine.DoConvertedAny(c.Request.Context(), oaiReq, "gemini")
	if err != nil {
		statusCode := errorStatusCode(err)
		c.JSON(statusCode, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
		}})
		finishRequestLog(logID, RequestLog{
			Protocol:        "gemini-inbound",
			Model:           modelName,
			Status:          statusCode,
			Duration:        time.Since(start).Milliseconds(),
			Stream:          isStream,
			Error:           err.Error(),
			Attempts:        attemptsFromError(err),
			Request:         geminiReq,
			UpstreamRequest: oaiReq,
			AccessKey:       requestAccessKey(c),
		})
		return
	}

	var promptTokens, completionTokens, totalTokens int
	if result.Response != nil {
		promptTokens = result.Response.Usage.PromptTokens
		completionTokens = result.Response.Usage.CompletionTokens
		totalTokens = result.Response.Usage.TotalTokens
	}
	finishRequestLog(logID, RequestLog{
		Protocol:         "gemini-inbound",
		Model:            modelName,
		ResolvedModel:    result.Model,
		Channel:          result.Channel,
		Status:           200,
		Duration:         time.Since(start).Milliseconds(),
		Stream:           isStream,
		Attempts:         result.Attempts,
		Request:          geminiReq,
		UpstreamRequest:  oaiReq,
		AccessKey:        requestAccessKey(c),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	})

	if isStream {
		h.handleGeminiStream(c, result)
		return
	}

	geminiResp := openAIToGemini(result.Response)
	c.JSON(http.StatusOK, geminiResp)
}

// ==================== Conversion Functions ====================

// Claude → OpenAI

type claudeInboundRequest struct {
	Model     string      `json:"model"`
	MaxTokens int         `json:"max_tokens"`
	System    interface{} `json:"system,omitempty"` // string or []content block
	Messages  []struct {
		Role    string      `json:"role"`
		Content interface{} `json:"content"`
	} `json:"messages"`
	Stream bool `json:"stream,omitempty"`
	Tools  []struct {
		Name        string      `json:"name"`
		Description string      `json:"description,omitempty"`
		InputSchema interface{} `json:"input_schema"`
	} `json:"tools,omitempty"`
}

// Responses handles POST /v1/responses (OpenAI Responses API format)
func (h *Protocol) Responses(c *gin.Context) {
	rawBody, readErr := io.ReadAll(c.Request.Body)
	if readErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + readErr.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	var req responsesInboundRequest
	if err := json.Unmarshal(rawBody, &req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": map[string]interface{}{
			"message": "invalid request: " + err.Error(),
			"type":    "invalid_request_error",
		}})
		return
	}
	if !requestCanUseModel(c, req.Model) {
		c.JSON(http.StatusForbidden, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("API key is not allowed to access model %q", req.Model),
			"type":    "permission_error",
			"param":   "model",
			"code":    "model_not_allowed",
		}})
		return
	}
	rawBody = h.applyRawResponsesSystemPrompt(rawBody, &req)

	logID := beginRequestLog(RequestLog{
		Protocol:  "responses",
		Model:     req.Model,
		Status:    102,
		Stream:    req.Stream,
		Request:   req,
		AccessKey: requestAccessKey(c),
	})
	if rawResult, ok := h.tryResponsesPassthrough(c, &req, rawBody, logID); ok {
		relayHandler := Relay{engine: h.engine}
		relayHandler.writeRawResponse(c, rawResult.Response)
		return
	}

	enableMiMoCompat := h.responsesMayUseMiMoCompat(req.Model)
	oaiReq := responsesToChatCompletion(&req, enableMiMoCompat)
	applyModelSystemPrompt(oaiReq, h.modelSystemPrompt(req.Model))

	start := time.Now()
	result, err := h.engine.DoConvertedAny(c.Request.Context(), oaiReq, "responses")
	if err != nil {
		statusCode := errorStatusCode(err)
		finishRequestLog(logID, RequestLog{
			Protocol:        "responses",
			Mode:            "converted",
			Model:           req.Model,
			Status:          statusCode,
			Duration:        time.Since(start).Milliseconds(),
			Stream:          req.Stream,
			Error:           err.Error(),
			Attempts:        attemptsFromError(err),
			Request:         req,
			UpstreamRequest: oaiReq,
			AccessKey:       requestAccessKey(c),
		})
		c.JSON(statusCode, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
			"type":    "upstream_error",
		}})
		return
	}

	var promptTokens, completionTokens, totalTokens int
	if result.Response != nil {
		promptTokens = result.Response.Usage.PromptTokens
		completionTokens = result.Response.Usage.CompletionTokens
		totalTokens = result.Response.Usage.TotalTokens
	}
	finishRequestLog(logID, RequestLog{
		Protocol:         "responses",
		Mode:             "converted",
		Model:            req.Model,
		ResolvedModel:    result.Model,
		Channel:          result.Channel,
		Status:           200,
		Duration:         time.Since(start).Milliseconds(),
		Stream:           req.Stream,
		Attempts:         result.Attempts,
		Request:          req,
		UpstreamRequest:  oaiReq,
		AccessKey:        requestAccessKey(c),
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		TotalTokens:      totalTokens,
	})

	enableMiMoCompat = enableMiMoCompat || (!result.DisableMiMoCompat && isMiMoCompatResult(result.Channel, req.Model, result.Model))
	if req.Stream {
		h.handleResponsesStream(c, result, req.Model, enableMiMoCompat)
		return
	}

	resp := chatCompletionToResponses(result.Response, req.Model, enableMiMoCompat)
	c.JSON(http.StatusOK, resp)
}

func (h *Protocol) tryResponsesPassthrough(c *gin.Context, req *responsesInboundRequest, rawBody []byte, logID int64) (*relay.RawRelayResult, bool) {
	start := time.Now()
	result, err := h.engine.DoRawResponses(c.Request.Context(), req.Model, req.Stream, rawBody, c.Request.Header)
	if err != nil {
		if errors.Is(err, relay.ErrNoAvailableChannel) {
			return nil, false
		}
		statusCode := errorStatusCode(err)
		c.JSON(statusCode, gin.H{"error": map[string]interface{}{
			"message": fmt.Sprintf("relay error: %v", err),
			"type":    "upstream_error",
		}})
		finishRequestLog(logID, RequestLog{
			Protocol:  "responses",
			Mode:      "passthrough",
			Model:     req.Model,
			Status:    statusCode,
			Duration:  time.Since(start).Milliseconds(),
			Stream:    req.Stream,
			Error:     err.Error(),
			Attempts:  attemptsFromError(err),
			Request:   req,
			AccessKey: requestAccessKey(c),
		})
		return nil, true
	}
	finishRequestLog(logID, RequestLog{
		Protocol:      "responses",
		Mode:          "passthrough",
		Model:         req.Model,
		ResolvedModel: result.Model,
		Channel:       result.Channel,
		Status:        result.Response.StatusCode,
		Duration:      time.Since(start).Milliseconds(),
		Stream:        req.Stream,
		Attempts:      result.Attempts,
		Request:       req,
		AccessKey:     requestAccessKey(c),
	})
	return result, true
}

func (h *Protocol) applyRawResponsesSystemPrompt(rawBody []byte, req *responsesInboundRequest) []byte {
	prompt := h.modelSystemPrompt(req.Model)
	if prompt == "" {
		return rawBody
	}
	reqCopy := *req
	if reqCopy.Instructions == "" {
		reqCopy.Instructions = prompt
	} else if !strings.Contains(reqCopy.Instructions, prompt) {
		reqCopy.Instructions = strings.TrimSpace(reqCopy.Instructions) + "\n\n" + prompt
	}
	data, err := json.Marshal(reqCopy)
	if err != nil {
		return rawBody
	}
	req.Instructions = reqCopy.Instructions
	return data
}

func (h *Protocol) modelSystemPrompt(modelName string) string {
	_, resolved, err := h.engine.PeekRoute(modelName)
	if err != nil {
		return systemPromptForModel(modelName, "")
	}
	return systemPromptForModel(modelName, resolved)
}

func (h *Protocol) applyRawClaudeSystemPrompt(rawBody []byte, req *claudeInboundRequest) []byte {
	prompt := h.modelSystemPrompt(req.Model)
	if prompt == "" {
		return rawBody
	}
	reqCopy := *req
	reqCopy.System = mergeClaudeSystemPrompt(req.System, prompt)
	data, err := json.Marshal(reqCopy)
	if err != nil {
		return rawBody
	}
	req.System = reqCopy.System
	return data
}

func mergeClaudeSystemPrompt(system interface{}, prompt string) interface{} {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return system
	}
	existing := extractSystemText(system)
	if strings.TrimSpace(existing) == "" {
		return prompt
	}
	if strings.Contains(existing, prompt) {
		return system
	}
	return existing + "\n\n" + prompt
}

func (h *Protocol) responsesMayUseMiMoCompat(modelName string) bool {
	ch, resolved, err := h.engine.PeekRoute(modelName)
	if err != nil {
		return isMiMoCompatModel(modelName)
	}
	return isMiMoCompatChannelResult(ch, modelName, resolved)
}
