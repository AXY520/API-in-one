package handler

import (
	"api-in-one/relay"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

func (h *Protocol) handleClaudeStream(c *gin.Context, result *relay.RelayResult) {
	if result.SSE != nil {
		defer result.SSE.Close()
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	writeClaudeEvent := func(event string, data interface{}) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		flusher.Flush()
	}

	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())
	blockIndex := 0
	textStarted := false
	textAccum := ""
	reasoningAccum := ""
	toolCallStates := map[int]*struct {
		blockIndex int
		callID     string
		name       string
		argsBuf    string
		started    bool
	}{}

	// message_start
	writeClaudeEvent("message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []interface{}{}, "model": "unknown",
			"stop_reason": nil, "usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})

	startTextBlock := func() {
		if textStarted {
			return
		}
		textStarted = true
		writeClaudeEvent("content_block_start", map[string]interface{}{
			"type": "content_block_start", "index": blockIndex,
			"content_block": map[string]interface{}{"type": "text", "text": ""},
		})
	}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		chunk := parseOpenAIChunk(data)
		if chunk == nil || len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Reasoning content
		if delta.ReasoningContent != "" {
			reasoningAccum += delta.ReasoningContent
		}

		// Text content
		if delta.Content != nil {
			if text, ok := delta.Content.(string); ok && text != "" {
				startTextBlock()
				textAccum += text
				writeClaudeEvent("content_block_delta", map[string]interface{}{
					"type": "content_block_delta", "index": blockIndex,
					"delta": map[string]interface{}{"type": "text_delta", "text": text},
				})
			}
		}

		// Tool calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				st, exists := toolCallStates[idx]
				if !exists {
					// Close text block if open
					if textStarted {
						writeClaudeEvent("content_block_stop", map[string]interface{}{
							"type": "content_block_stop", "index": blockIndex,
						})
						blockIndex++
						textStarted = false
					}
					st = &struct {
						blockIndex int
						callID     string
						name       string
						argsBuf    string
						started    bool
					}{
						blockIndex: blockIndex,
						callID:     tc.ID,
						name:       tc.Function.Name,
					}
					if st.callID == "" {
						st.callID = fmt.Sprintf("call_%d", time.Now().UnixMilli())
					}
					toolCallStates[idx] = st
					blockIndex++
				}
				if tc.Function.Name != "" && st.name == "" {
					st.name = tc.Function.Name
				}
				if !st.started && st.name != "" {
					st.started = true
					writeClaudeEvent("content_block_start", map[string]interface{}{
						"type": "content_block_start", "index": st.blockIndex,
						"content_block": map[string]interface{}{
							"type": "tool_use", "id": st.callID, "name": st.name,
						},
					})
				}
				if tc.Function.Arguments != "" {
					st.argsBuf += tc.Function.Arguments
					if st.started {
						writeClaudeEvent("content_block_delta", map[string]interface{}{
							"type": "content_block_delta", "index": st.blockIndex,
							"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": tc.Function.Arguments},
						})
					}
				}
			}
		}

		if chunk.Choices[0].FinishReason != nil {
			break
		}
	}

	// Finalize text block
	if textStarted {
		writeClaudeEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": blockIndex,
		})
		blockIndex++
	}

	// Finalize tool call blocks
	for _, st := range toolCallStates {
		if st.name == "" {
			st.name = "tool"
		}
		if !st.started {
			writeClaudeEvent("content_block_start", map[string]interface{}{
				"type": "content_block_start", "index": st.blockIndex,
				"content_block": map[string]interface{}{
					"type": "tool_use", "id": st.callID, "name": st.name,
				},
			})
			if st.argsBuf != "" {
				writeClaudeEvent("content_block_delta", map[string]interface{}{
					"type": "content_block_delta", "index": st.blockIndex,
					"delta": map[string]interface{}{"type": "input_json_delta", "partial_json": st.argsBuf},
				})
			}
		}
		writeClaudeEvent("content_block_stop", map[string]interface{}{
			"type": "content_block_stop", "index": st.blockIndex,
		})
	}

	// message_delta with stop_reason
	stopReason := "end_turn"
	if len(toolCallStates) > 0 {
		stopReason = "tool_use"
	}
	writeClaudeEvent("message_delta", map[string]interface{}{
		"type":  "message_delta",
		"delta": map[string]interface{}{"stop_reason": stopReason},
		"usage": map[string]interface{}{"output_tokens": 0},
	})

	// message_stop
	writeClaudeEvent("message_stop", map[string]interface{}{"type": "message_stop"})
}
func (h *Protocol) handleGeminiStream(c *gin.Context, result *relay.RelayResult) {
	if result.SSE != nil {
		defer result.SSE.Close()
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			return
		}
		if err != nil {
			return
		}

		chunk := parseOpenAIChunk(data)
		if chunk == nil {
			continue
		}

		text := ""
		var finishReason *string
		if len(chunk.Choices) > 0 && chunk.Choices[0].Delta != nil {
			if chunk.Choices[0].Delta.Content != nil {
				text, _ = chunk.Choices[0].Delta.Content.(string)
			}
		}
		if len(chunk.Choices) > 0 && chunk.Choices[0].FinishReason != nil {
			fr := mapFinishReasonToGemini(*chunk.Choices[0].FinishReason)
			finishReason = &fr
		}

		resp := map[string]interface{}{
			"candidates": []map[string]interface{}{{
				"content": map[string]interface{}{
					"parts": []map[string]interface{}{{"text": text}},
					"role":  "model",
				},
				"finishReason": finishReason,
			}},
		}

		b, _ := json.Marshal(resp)
		c.Writer.Write([]byte("data: " + string(b) + "\n\n"))
		flusher.Flush()

		if finishReason != nil {
			return
		}
	}
}
func (h *Protocol) handleResponsesStream(c *gin.Context, result *relay.RelayResult, modelName string, enableMiMoCompat bool) {
	if result.SSE != nil {
		defer result.SSE.Close()
	}
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Status(http.StatusOK)

	flusher, ok := c.Writer.(http.Flusher)
	if !ok {
		return
	}

	respID := fmt.Sprintf("resp_%d", time.Now().UnixMilli())
	outputIndex := 0
	reasonID := fmt.Sprintf("reason_%d", time.Now().UnixMilli())
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixMilli())

	writeEvent := func(event string, data map[string]interface{}) {
		data["type"] = event
		b, _ := json.Marshal(data)
		fmt.Fprintf(c.Writer, "event: %s\ndata: %s\n\n", event, string(b))
		flusher.Flush()
	}

	writeEvent("response.created", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response", "status": "in_progress", "model": modelName, "output": []interface{}{},
		},
	})

	reasoningAccum := ""
	textAccum := ""
	reasoningStarted := false
	messageStarted := false

	startReasoning := func() {
		if reasoningStarted {
			return
		}
		reasoningStarted = true
		writeEvent("response.output_item.added", map[string]interface{}{
			"output_index": outputIndex,
			"item": map[string]interface{}{
				"id": reasonID, "type": "reasoning", "summary": []interface{}{}, "status": "in_progress",
			},
		})
		writeEvent("response.reasoning_summary_part.added", map[string]interface{}{
			"item_id": reasonID, "output_index": outputIndex, "summary_index": 0,
			"part": map[string]interface{}{"type": "summary_text", "text": ""},
		})
	}

	startMessage := func() {
		if messageStarted {
			return
		}
		if reasoningStarted {
			// Finalize reasoning first
			writeEvent("response.reasoning_summary_text.done", map[string]interface{}{
				"item_id": reasonID, "output_index": outputIndex, "summary_index": 0, "text": reasoningAccum,
			})
			writeEvent("response.reasoning_summary_part.done", map[string]interface{}{
				"item_id": reasonID, "output_index": outputIndex, "summary_index": 0,
				"part": map[string]interface{}{"type": "summary_text", "text": reasoningAccum},
			})
			writeEvent("response.output_item.done", map[string]interface{}{
				"output_index": outputIndex,
				"item": map[string]interface{}{
					"id": reasonID, "type": "reasoning",
					"summary":           []map[string]interface{}{{"type": "summary_text", "text": reasoningAccum}},
					"encrypted_content": reasoningAccum, "status": "completed",
				},
			})
			outputIndex++
		}
		messageStarted = true
		writeEvent("response.output_item.added", map[string]interface{}{
			"output_index": outputIndex,
			"item": map[string]interface{}{
				"id": msgID, "type": "message", "role": "assistant", "status": "in_progress", "content": []interface{}{},
			},
		})
		writeEvent("response.content_part.added", map[string]interface{}{
			"output_index": outputIndex, "content_index": 0,
			"part": map[string]interface{}{"type": "output_text", "text": ""},
		})
	}

	output := []map[string]interface{}{}

	// Track tool_calls state
	type toolCallState struct {
		itemID   string
		outIndex int
		callID   string
		name     string
		argsBuf  string
	}
	toolCalls := map[int]*toolCallState{}

	for {
		data, err := result.SSE.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			break
		}

		chunk := parseOpenAIChunk(data)
		if chunk == nil || len(chunk.Choices) == 0 || chunk.Choices[0].Delta == nil {
			continue
		}

		delta := chunk.Choices[0].Delta

		// Reasoning content
		if delta.ReasoningContent != "" {
			startReasoning()
			reasoningAccum += delta.ReasoningContent
			writeEvent("response.reasoning_summary_text.delta", map[string]interface{}{
				"item_id": reasonID, "output_index": outputIndex, "summary_index": 0, "delta": delta.ReasoningContent,
			})
		}

		// Text content
		text := ""
		if delta.Content != nil {
			text, _ = delta.Content.(string)
		}
		if text != "" {
			textAccum += text
		}

		// Tool calls
		if len(delta.ToolCalls) > 0 {
			for _, tc := range delta.ToolCalls {
				idx := tc.Index
				st, exists := toolCalls[idx]
				if !exists {
					// New tool call
					st = &toolCallState{
						itemID:   fmt.Sprintf("fc_%d_%d", time.Now().UnixMilli(), idx),
						outIndex: outputIndex,
						callID:   tc.ID,
						name:     tc.Function.Name,
					}
					if st.callID == "" {
						st.callID = fmt.Sprintf("call_%s", st.itemID[3:])
					}
					toolCalls[idx] = st
					outputIndex++

					writeEvent("response.output_item.added", map[string]interface{}{
						"output_index": st.outIndex,
						"item": map[string]interface{}{
							"id": st.itemID, "type": "function_call", "call_id": st.callID,
							"name": st.name, "arguments": "", "status": "in_progress",
						},
					})
				}
				if tc.Function.Name != "" && st.name == "" {
					st.name = tc.Function.Name
				}
				if tc.Function.Arguments != "" {
					st.argsBuf += tc.Function.Arguments
					writeEvent("response.function_call_arguments.delta", map[string]interface{}{
						"item_id": st.itemID, "output_index": st.outIndex, "delta": tc.Function.Arguments,
					})
				}
			}
		}

		if chunk.Choices[0].FinishReason != nil {
			break
		}
	}

	// Finalize tool calls
	for _, st := range toolCalls {
		safeArgs := st.argsBuf
		if safeArgs == "" {
			safeArgs = "{}"
		} else if !json.Valid([]byte(safeArgs)) {
			safeArgs = "{}"
		}
		writeEvent("response.function_call_arguments.done", map[string]interface{}{
			"item_id": st.itemID, "output_index": st.outIndex, "arguments": safeArgs,
		})
		output = append(output, map[string]interface{}{
			"id": st.itemID, "type": "function_call", "call_id": st.callID,
			"name": st.name, "arguments": safeArgs, "status": "completed",
		})
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": st.outIndex, "item": map[string]interface{}{
				"id": st.itemID, "type": "function_call", "call_id": st.callID,
				"name": st.name, "arguments": safeArgs, "status": "completed",
			},
		})
	}

	var fakeCalls []fakeToolCall
	if enableMiMoCompat {
		textAccum, fakeCalls = extractFakeToolCalls(textAccum)
	}
	for i, call := range fakeCalls {
		itemID := fmt.Sprintf("fc_fake_%d_%d", time.Now().UnixMilli(), i)
		callID := fmt.Sprintf("call_%s", itemID[3:])
		outIndex := outputIndex
		outputIndex++
		writeEvent("response.output_item.added", map[string]interface{}{
			"output_index": outIndex,
			"item": map[string]interface{}{
				"id": itemID, "type": "function_call", "call_id": callID,
				"name": call.Name, "arguments": "", "status": "in_progress",
			},
		})
		writeEvent("response.function_call_arguments.delta", map[string]interface{}{
			"item_id": itemID, "output_index": outIndex, "delta": call.Arguments,
		})
		writeEvent("response.function_call_arguments.done", map[string]interface{}{
			"item_id": itemID, "output_index": outIndex, "arguments": call.Arguments,
		})
		item := map[string]interface{}{
			"id": itemID, "type": "function_call", "call_id": callID,
			"name": call.Name, "arguments": call.Arguments, "status": "completed",
		}
		output = append(output, item)
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": outIndex, "item": item,
		})
	}

	// Finalize text message (only if there was text)
	textAccum = strings.TrimSpace(textAccum)
	if textAccum != "" || len(toolCalls) == 0 && len(fakeCalls) == 0 {
		if !messageStarted {
			startMessage()
		}
		if textAccum != "" {
			writeEvent("response.output_text.delta", map[string]interface{}{
				"output_index": outputIndex, "content_index": 0, "delta": textAccum,
			})
		}
		writeEvent("response.output_text.done", map[string]interface{}{
			"output_index": outputIndex, "content_index": 0, "text": textAccum,
		})
		writeEvent("response.content_part.done", map[string]interface{}{
			"output_index": outputIndex, "content_index": 0,
			"part": map[string]interface{}{"type": "output_text", "text": textAccum},
		})
		msgItem := map[string]interface{}{
			"id": msgID, "type": "message", "role": "assistant", "status": "completed",
			"content": []map[string]interface{}{{"type": "output_text", "text": textAccum}},
		}
		writeEvent("response.output_item.done", map[string]interface{}{
			"output_index": outputIndex, "item": msgItem,
		})
		output = append(output, msgItem)
	}

	// response.completed
	writeEvent("response.completed", map[string]interface{}{
		"response": map[string]interface{}{
			"id": respID, "object": "response", "status": "completed", "model": modelName, "output": output,
		},
	})
}
