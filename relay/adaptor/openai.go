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
)

// OpenAIAdaptor handles OpenAI-compatible APIs (OpenAI, DeepSeek, etc.)
type OpenAIAdaptor struct{}

func (a *OpenAIAdaptor) Name() string { return "openai" }

func (a *OpenAIAdaptor) BuildHTTPRequest(baseURL, key string, req *model.ChatCompletionRequest) (*http.Request, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	httpReq, err := http.NewRequest("POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+key)
	return httpReq, nil
}

func (a *OpenAIAdaptor) ParseResponse(resp *http.Response) (*model.ChatCompletionResponse, error) {
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream error (status %d): %s", resp.StatusCode, string(body))
	}
	var result model.ChatCompletionResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (a *OpenAIAdaptor) StreamHandler(resp *http.Response) SSEProcessor {
	return &openAISSEProcessor{
		scanner: bufio.NewScanner(resp.Body),
		body:    resp.Body,
	}
}

// ---- OpenAI SSE Processor ----

type openAISSEProcessor struct {
	scanner *bufio.Scanner
	body    io.ReadCloser
}

func (p *openAISSEProcessor) Next() ([]byte, error) {
	for p.scanner.Scan() {
		line := p.scanner.Text()
		if line == "" {
			continue
		}
		if strings.HasPrefix(line, "data: ") {
			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				return nil, io.EOF
			}
			// Validate it's valid JSON and pass through
			var chunk model.ChatCompletionChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // skip malformed events
			}
			return []byte("data: " + data + "\n\n"), nil
		}
	}
	if err := p.scanner.Err(); err != nil {
		return nil, err
	}
	return nil, io.EOF
}
