package handler

import (
	"api-in-one/model"
	"api-in-one/relay"
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
	var req model.ChatCompletionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
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
	result, err := h.engine.Do(c.Request.Context(), &req, "openai")
	if err != nil {
		slog.Error("relay failed", "model", req.Model, "error", err, "took", time.Since(start))
		logRequest("openai", req.Model, 502, time.Since(start), err)
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
	logRequest("openai", req.Model, 200, time.Since(start), nil)

	if result.IsStream {
		h.handleStream(c, result)
		return
	}

	c.JSON(http.StatusOK, result.Response)
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
