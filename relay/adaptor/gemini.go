package adaptor

import (
	"api-in-one/model"
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// GeminiAdaptor handles Google Gemini API format conversion.
type GeminiAdaptor struct{}

// ---- Gemini native request/response structures ----

type geminiRequest struct {
	Contents         []geminiContent        `json:"contents"`
	SystemInstruction *geminiContent         `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool           `json:"tools,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text string `json:"text,omitempty"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens int     `json:"maxOutputTokens,omitempty"`
	Temperature     float64 `json:"temperature,omitempty"`
	TopP            float64 `json:"topP,omitempty"`
	StopSequences   []string `json:"stopSequences,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"function_declarations"`
}

type geminiFunctionDecl struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	Parameters  interface{} `json:"parameters,omitempty"`
}

type geminiResponse struct {
	Candidates    []geminiCandidate   `json:"candidates"`
	UsageMetadata geminiUsageMetadata `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content       geminiContent `json:"content"`
	FinishReason  string        `json:"finishReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}

func (a *GeminiAdaptor) Name() string { return "gemini" }

func (a *GeminiAdaptor) BuildHTTPRequest(baseURL, key string, req *model.ChatCompletionRequest) (*http.Request, error) {
	geminiReq := a.convertRequest(req)
	body, err := json.Marshal(geminiReq)
	if err != nil {
		return nil, err
	}

	endpoint := "generateContent"
	if req.Stream {
		endpoint = "streamGenerateContent?alt=sse"
	}
	url := fmt.Sprintf("%s/v1beta/models/%s:%s", strings.TrimRight(baseURL, "/"), req.Model, endpoint)

	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("x-goog-api-key", key)
	return httpReq, nil
}

func (a *GeminiAdaptor) convertRequest(req *model.ChatCompletionRequest) *geminiRequest {
	gr := &geminiRequest{}

	for _, msg := range req.Messages {
		if msg.Role == "system" {
			gr.SystemInstruction = &geminiContent{
				Parts: []geminiPart{{Text: extractTextContent(msg.Content)}},
			}
			continue
		}
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		gc := geminiContent{
			Role:  role,
			Parts: []geminiPart{{Text: extractTextContent(msg.Content)}},
		}
		gr.Contents = append(gr.Contents, gc)
	}

	// Generation config
	gc := &geminiGenerationConfig{}
	if req.MaxTokens != nil {
		gc.MaxOutputTokens = *req.MaxTokens
	}
	if req.Temperature != nil {
		gc.Temperature = *req.Temperature
	}
	if req.TopP != nil {
		gc.TopP = *req.TopP
	}
	if len(req.Stop) > 0 {
		gc.StopSequences = req.Stop
	}
	gr.GenerationConfig = gc

	// Convert tools
	for _, tool := range req.Tools {
		fd := geminiFunctionDecl{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  tool.Function.Parameters,
		}
		if len(gr.Tools) == 0 {
			gr.Tools = []geminiTool{{}}
		}
		gr.Tools[0].FunctionDeclarations = append(gr.Tools[0].FunctionDeclarations, fd)
	}

	return gr
}

func (a *GeminiAdaptor) ParseResponse(resp *http.Response) (*model.ChatCompletionResponse, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("gemini upstream error (status %d): %s", resp.StatusCode, string(body))
	}
	var geminiResp geminiResponse
	if err := json.Unmarshal(body, &geminiResp); err != nil {
		return nil, err
	}
	return a.convertResponse(&geminiResp, ""), nil
}

func (a *GeminiAdaptor) convertResponse(gr *geminiResponse, modelName string) *model.ChatCompletionResponse {
	text := ""
	finishReason := "stop"
	if len(gr.Candidates) > 0 {
		for _, part := range gr.Candidates[0].Content.Parts {
			text += part.Text
		}
		finishReason = mapGeminiStopReason(gr.Candidates[0].FinishReason)
	}
	return &model.ChatCompletionResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().UnixMilli()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []model.Choice{{
			Index: 0,
			Message: &model.Message{
				Role:    "assistant",
				Content: text,
			},
			FinishReason: &finishReason,
		}},
		Usage: model.Usage{
			PromptTokens:     gr.UsageMetadata.PromptTokenCount,
			CompletionTokens: gr.UsageMetadata.CandidatesTokenCount,
			TotalTokens:      gr.UsageMetadata.TotalTokenCount,
		},
	}
}

func (a *GeminiAdaptor) StreamHandler(resp *http.Response) SSEProcessor {
	return &geminiSSEProcessor{
		scanner: bufio.NewScanner(resp.Body),
		body:    resp.Body,
	}
}

// ---- Gemini SSE Processor ----

type geminiSSEProcessor struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
}

func (p *geminiSSEProcessor) Next() ([]byte, error) {
	for p.scanner.Scan() {
		line := p.scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")

		var geminiResp geminiResponse
		if err := json.Unmarshal([]byte(data), &geminiResp); err != nil {
			continue
		}

		text := ""
		var finishReason *string
		if len(geminiResp.Candidates) > 0 {
			for _, part := range geminiResp.Candidates[0].Content.Parts {
				text += part.Text
			}
			if geminiResp.Candidates[0].FinishReason != "" {
				fr := mapGeminiStopReason(geminiResp.Candidates[0].FinishReason)
				finishReason = &fr
			}
		}

		chunk := model.ChatCompletionChunk{
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Choices: []model.ChunkChoice{{
				Index:        0,
				Delta:        &model.Message{Content: text},
				FinishReason: finishReason,
			}},
		}

		if finishReason != nil {
			b, _ := json.Marshal(chunk)
			return []byte("data: " + string(b) + "\n\n"), io.EOF
		}

		b, _ := json.Marshal(chunk)
		return []byte("data: " + string(b) + "\n\n"), nil
	}
	if err := p.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}

func mapGeminiStopReason(reason string) string {
	switch reason {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY":
		return "content_filter"
	default:
		return "stop"
	}
}
