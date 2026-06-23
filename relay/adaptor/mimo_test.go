package adaptor

import (
	"api-in-one/model"
	"testing"
)

func TestExtractFakeToolCalls(t *testing.T) {
	text := `Hello, I will call the search tool.
<tool_call>
<function=google_search>
<parameter=query>weather today</parameter>
</function>
</tool_call>
Let me know if you need anything else.`

	cleaned, calls := ExtractFakeToolCalls(text)
	if len(calls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(calls))
	}
	if calls[0].Name != "google_search" {
		t.Errorf("expected tool name google_search, got %s", calls[0].Name)
	}
	expectedArgs := `{"query":"weather today"}`
	if calls[0].Arguments != expectedArgs {
		t.Errorf("expected arguments %s, got %s", expectedArgs, calls[0].Arguments)
	}
	if cleaned != "Hello, I will call the search tool.\n\nLet me know if you need anything else." {
		t.Errorf("unexpected cleaned text: %q", cleaned)
	}
}

func TestCleanResponseToolCalls(t *testing.T) {
	resp := &model.ChatCompletionResponse{
		Choices: []model.Choice{
			{
				Message: &model.Message{
					Role: "assistant",
					Content: `Some reasoning
<tool_call>
<function=calculator>
<parameter=expr>2+2</parameter>
</function>
</tool_call>`,
				},
			},
		},
	}

	CleanResponseToolCalls(resp)
	content := resp.Choices[0].Message.Content.(string)
	if content != "Some reasoning" {
		t.Errorf("expected cleaned content 'Some reasoning', got %q", content)
	}
}
