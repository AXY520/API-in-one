package handler

import (
	"api-in-one/model"
	"api-in-one/relay"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
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

	if len(req.Messages) == 0 {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{
			Error: model.Error{
				Message: "messages array is required and must not be empty",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	start := time.Now()
	result, err := h.engine.DoRaw(c.Request.Context(), "openai", req.Model, req.Stream, rawBody, c.Request.Header)
	if err != nil {
		attempts := attemptsFromError(err)
		slog.Error("relay failed", "model", req.Model, "error", err, "took", time.Since(start))
		logRequestDetail(RequestLog{
			Protocol:  "openai",
			Model:     req.Model,
			Status:    502,
			Duration:  time.Since(start).Milliseconds(),
			Stream:    req.Stream,
			Error:     err.Error(),
			Attempts:  attempts,
			Request:   req,
			AccessKey: requestAccessKey(c),
		})
		c.JSON(http.StatusBadGateway, model.ErrorResponse{
			Error: model.Error{
				Message: fmt.Sprintf("relay error: %v", err),
				Type:    "upstream_error",
			},
		})
		return
	}

	slog.Info("relay success",
		"model", req.Model,
		"channel", result.Channel,
		"resolved_model", result.Model,
		"stream", req.Stream,
		"took", time.Since(start),
	)
	logRequestDetail(RequestLog{
		Protocol:      "openai",
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

	h.writeRawResponse(c, result.Response)
}

func requestAccessKey(c *gin.Context) string {
	if v, ok := c.Get("api_key_masked"); ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func attemptsFromError(err error) []relay.AttemptLog {
	var relayErr *relay.RelayError
	if errors.As(err, &relayErr) {
		return relayErr.Attempts
	}
	return nil
}

func (h *Relay) handleStream(c *gin.Context, result *relay.RelayResult) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("X-Accel-Buffering", "no")

	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		c.JSON(http.StatusInternalServerError, model.ErrorResponse{
			Error: model.Error{
				Message: "streaming not supported",
				Type:    "server_error",
			},
		})
		return
	}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			// Send final [DONE] marker
			fmt.Fprintf(c.Writer, "data: [DONE]\n\n")
			flusher.Flush()
			return
		}
		if err != nil {
			slog.Error("stream read error", "error", err)
			return
		}

		_, writeErr := c.Writer.Write(data)
		if writeErr != nil {
			return
		}
		flusher.Flush()
	}
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
