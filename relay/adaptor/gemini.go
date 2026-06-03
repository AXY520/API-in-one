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
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"systemInstruction,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	ToolConfig        *geminiToolConfig       `json:"toolConfig,omitempty"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	InlineData       *geminiInlineData       `json:"inlineData,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mimeType"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string                 `json:"name"`
	Args map[string]interface{} `json:"args"`
}

type geminiFunctionResponse struct {
	Name     string                 `json:"name"`
	Response map[string]interface{} `json:"response"`
}

type geminiGenerationConfig struct {
	MaxOutputTokens  int         `json:"maxOutputTokens,omitempty"`
	Temperature      float64     `json:"temperature,omitempty"`
	TopP             float64     `json:"topP,omitempty"`
	StopSequences    []string    `json:"stopSequences,omitempty"`
	ResponseMimeType string      `json:"responseMimeType,omitempty"`
	ResponseSchema   interface{} `json:"responseSchema,omitempty"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"function_declarations"`
}

type geminiToolConfig struct {
	FunctionCallingConfig map[string]interface{} `json:"functionCallingConfig,omitempty"`
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
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
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
				Parts: buildGeminiParts(msg.Content),
			}
			continue
		}
		role := msg.Role
		if role == "assistant" {
			role = "model"
		}
		if role == "model" {
			parts := buildGeminiParts(msg.Content)
			for _, tc := range msg.ToolCalls {
				var args map[string]interface{}
				if tc.Function.Arguments != "" {
					_ = json.Unmarshal([]byte(tc.Function.Arguments), &args)
				}
				parts = append(parts, geminiPart{
					FunctionCall: &geminiFunctionCall{
						Name: tc.Function.Name,
						Args: args,
					},
				})
			}
			gr.Contents = append(gr.Contents, geminiContent{
				Role:  "model",
				Parts: parts,
			})
			continue
		}
		if role == "tool" {
			var respObj map[string]interface{}
			contentStr := extractTextContent(msg.Content)
			if err := json.Unmarshal([]byte(contentStr), &respObj); err != nil {
				respObj = map[string]interface{}{"result": contentStr}
			}
			name := msg.Name
			if name == "" {
				name = msg.ToolCallID
			}
			gr.Contents = append(gr.Contents, geminiContent{
				Role: "user",
				Parts: []geminiPart{{
					FunctionResponse: &geminiFunctionResponse{
						Name:     name,
						Response: respObj,
					},
				}},
			})
			continue
		}
		gc := geminiContent{
			Role:  role,
			Parts: buildGeminiParts(msg.Content),
		}
		if len(gc.Parts) > 0 {
			gr.Contents = append(gr.Contents, gc)
		}
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
		if len(gc.StopSequences) > 5 {
			gc.StopSequences = gc.StopSequences[:5]
		}
	}
	applyGeminiResponseFormat(gc, req.ResponseFormat)
	gr.GenerationConfig = gc

	// Convert tools
	for _, tool := range req.Tools {
		fd := geminiFunctionDecl{
			Name:        tool.Function.Name,
			Description: tool.Function.Description,
			Parameters:  cleanGeminiSchema(tool.Function.Parameters),
		}
		if len(gr.Tools) == 0 {
			gr.Tools = []geminiTool{{}}
		}
		gr.Tools[0].FunctionDeclarations = append(gr.Tools[0].FunctionDeclarations, fd)
	}
	gr.ToolConfig = openAIToGeminiToolConfig(req.ToolChoice)

	return gr
}

func buildGeminiParts(content interface{}) []geminiPart {
	switch v := content.(type) {
	case []model.ContentPart:
		var parts []geminiPart
		for _, part := range v {
			switch part.Type {
			case "text":
				if part.Text != "" {
					parts = append(parts, geminiPart{Text: part.Text})
				}
			case "image_url":
				if part.ImageURL == nil {
					continue
				}
				if mimeType, data, ok := parseDataURL(part.ImageURL.URL); ok {
					parts = append(parts, geminiPart{InlineData: &geminiInlineData{MimeType: mimeType, Data: data}})
				}
			}
		}
		return parts
	default:
		text := extractTextContent(content)
		if text == "" {
			return nil
		}
		return []geminiPart{{Text: text}}
	}
}

func parseDataURL(raw string) (string, string, bool) {
	if !strings.HasPrefix(raw, "data:") {
		return "", "", false
	}
	header, data, ok := strings.Cut(strings.TrimPrefix(raw, "data:"), ",")
	if !ok || !strings.Contains(header, ";base64") || data == "" {
		return "", "", false
	}
	mimeType := strings.TrimSuffix(header, ";base64")
	if mimeType == "" {
		return "", "", false
	}
	return mimeType, data, true
}

func openAIToGeminiToolConfig(choice interface{}) *geminiToolConfig {
	var cfg map[string]interface{}
	switch v := choice.(type) {
	case string:
		switch v {
		case "auto":
			cfg = map[string]interface{}{"mode": "AUTO"}
		case "none":
			cfg = map[string]interface{}{"mode": "NONE"}
		case "required":
			cfg = map[string]interface{}{"mode": "ANY"}
		}
	case map[string]interface{}:
		if fn, ok := v["function"].(map[string]interface{}); ok {
			if name, ok := fn["name"].(string); ok && name != "" {
				cfg = map[string]interface{}{"mode": "ANY", "allowedFunctionNames": []string{name}}
			}
		}
	}
	if cfg == nil {
		return nil
	}
	return &geminiToolConfig{FunctionCallingConfig: cfg}
}

func applyGeminiResponseFormat(gc *geminiGenerationConfig, responseFormat interface{}) {
	format, ok := responseFormat.(map[string]interface{})
	if !ok {
		return
	}
	formatType, _ := format["type"].(string)
	if formatType != "json_object" && formatType != "json_schema" {
		return
	}
	gc.ResponseMimeType = "application/json"
	if jsonSchema, ok := format["json_schema"].(map[string]interface{}); ok {
		if schema, ok := jsonSchema["schema"]; ok {
			gc.ResponseSchema = cleanGeminiSchema(schema)
		}
	}
}

func cleanGeminiSchema(value interface{}) interface{} {
	switch v := value.(type) {
	case map[string]interface{}:
		allowed := map[string]bool{
			"anyOf": true, "default": true, "description": true, "enum": true,
			"format": true, "items": true, "maxItems": true, "maxLength": true,
			"maximum": true, "minimum": true, "minItems": true, "minLength": true,
			"nullable": true, "properties": true, "required": true, "title": true, "type": true,
		}
		out := map[string]interface{}{}
		for k, val := range v {
			if !allowed[k] {
				continue
			}
			out[k] = cleanGeminiSchema(val)
		}
		if typ, ok := out["type"].(string); ok {
			switch strings.ToLower(typ) {
			case "object":
				out["type"] = "OBJECT"
			case "array":
				out["type"] = "ARRAY"
			case "string":
				out["type"] = "STRING"
			case "integer":
				out["type"] = "INTEGER"
			case "number":
				out["type"] = "NUMBER"
			case "boolean":
				out["type"] = "BOOLEAN"
			}
		}
		return out
	case []interface{}:
		out := make([]interface{}, 0, len(v))
		for _, item := range v {
			out = append(out, cleanGeminiSchema(item))
		}
		return out
	default:
		return value
	}
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
	var toolCalls []model.ToolCall
	if len(gr.Candidates) > 0 {
		for _, part := range gr.Candidates[0].Content.Parts {
			if part.Text != "" {
				text += part.Text
			}
			if part.FunctionCall != nil {
				argsBytes, _ := json.Marshal(part.FunctionCall.Args)
				toolCalls = append(toolCalls, model.ToolCall{
					ID:   fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, time.Now().UnixMilli()),
					Type: "function",
					Function: model.FunctionCall{
						Name:      part.FunctionCall.Name,
						Arguments: string(argsBytes),
					},
				})
			}
		}
		if len(toolCalls) > 0 {
			finishReason = "tool_calls"
		} else if gr.Candidates[0].FinishReason != "" {
			finishReason = mapGeminiStopReason(gr.Candidates[0].FinishReason)
		}
	}
	msg := &model.Message{
		Role:    "assistant",
		Content: text,
	}
	if len(toolCalls) > 0 {
		msg.ToolCalls = toolCalls
	}
	return &model.ChatCompletionResponse{
		ID:      fmt.Sprintf("gemini-%d", time.Now().UnixMilli()),
		Object:  "chat.completion",
		Created: time.Now().Unix(),
		Model:   modelName,
		Choices: []model.Choice{{
			Index:        0,
			Message:      msg,
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
		var toolCalls []model.ToolCall
		var finishReason *string
		if len(geminiResp.Candidates) > 0 {
			for _, part := range geminiResp.Candidates[0].Content.Parts {
				if part.Text != "" {
					text += part.Text
				}
				if part.FunctionCall != nil {
					argsBytes, _ := json.Marshal(part.FunctionCall.Args)
					toolCalls = append(toolCalls, model.ToolCall{
						ID:   fmt.Sprintf("call_%s_%d", part.FunctionCall.Name, time.Now().UnixMilli()),
						Type: "function",
						Function: model.FunctionCall{
							Name:      part.FunctionCall.Name,
							Arguments: string(argsBytes),
						},
					})
				}
			}
			if len(toolCalls) > 0 {
				fr := "tool_calls"
				finishReason = &fr
			} else if geminiResp.Candidates[0].FinishReason != "" {
				fr := mapGeminiStopReason(geminiResp.Candidates[0].FinishReason)
				finishReason = &fr
			}
		}

		delta := &model.Message{Content: text}
		if len(toolCalls) > 0 {
			delta.ToolCalls = toolCalls
		}

		chunk := model.ChatCompletionChunk{
			Object:  "chat.completion.chunk",
			Created: time.Now().Unix(),
			Choices: []model.ChunkChoice{{
				Index:        0,
				Delta:        delta,
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
	switch strings.ToUpper(reason) {
	case "STOP":
		return "stop"
	case "MAX_TOKENS":
		return "length"
	case "SAFETY", "RECITATION", "BLOCKLIST", "PROHIBITED_CONTENT", "SPII", "MALFORMED_FUNCTION_CALL":
		return "content_filter"
	default:
		return "stop"
	}
}
