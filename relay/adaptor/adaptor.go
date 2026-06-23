package adaptor

import (
	"api-in-one/model"
	"net/http"
)

const UpstreamUserAgent = "API-in-one/1.0"

func SetUpstreamHeaders(req *http.Request) {
	req.Header.Set("User-Agent", UpstreamUserAgent)
}

// RelayRequest contains everything needed to forward a request upstream.
type RelayRequest struct {
	Body    *model.ChatCompletionRequest
	Channel *model.Channel
}

// Adaptor converts between OpenAI format and a provider's native format.
type Adaptor interface {
	// Name returns the provider type name.
	Name() string

	// BuildHTTPRequest creates the upstream HTTP request.
	// The body should be in the provider's native format.
	BuildHTTPRequest(baseURL string, key string, req *model.ChatCompletionRequest) (*http.Request, error)

	// ParseResponse parses a non-streaming upstream response into OpenAI format.
	ParseResponse(resp *http.Response) (*model.ChatCompletionResponse, error)

	// StreamHandler returns an SSE processor that converts provider-specific
	// SSE events to OpenAI-compatible chunks. Returns nil if not supported.
	StreamHandler(resp *http.Response) SSEProcessor
}

// SSEProcessor reads provider SSE events and yields OpenAI-format chunks.
type SSEProcessor interface {
	// Next returns the next OpenAI-format SSE data line, or io.EOF.
	// The returned bytes should be a complete "data: {...}\n\n" SSE event.
	Next() ([]byte, error)

	// Close releases any underlying resources (such as HTTP response body).
	Close() error
}
