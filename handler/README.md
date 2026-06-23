# Handler Layout

- `relay.go`
  - OpenAI Chat inbound handler
  - OpenAI/Responses upstream raw response adapters
- `protocol.go`
  - Claude / Gemini / Responses inbound handlers
  - stream output writers
  - request-level system prompt injection helpers
- `protocol_converters.go`
  - protocol conversion helpers
  - Claude <-> OpenAI
  - Gemini <-> OpenAI
- `responses_convert.go`
  - Responses API <-> OpenAI Chat conversion helpers
- `admin.go`
  - admin APIs
- `log.go`
  - request log storage and querying

## Routing Boundary

Handlers should:

1. parse inbound protocol payload
2. apply access control and request-level prompt logic
3. convert inbound payload into `model.ChatCompletionRequest` when needed
4. delegate channel selection and upstream protocol choice to `relay.Engine`
5. convert the normalized OpenAI-style result back to the caller protocol

Handlers should not implement channel selection policy.

## Conversion Boundary

- `handler/*converter*`:
  inbound/outbound protocol payload shape conversion
- `relay/engine.go`:
  channel selection, retry, and final upstream protocol execution
- `relay/adaptor/*`:
  provider-specific HTTP request/response translation
