package adaptor

import "testing"

func TestBuildOpenAIChatCompletionsURLAvoidsDuplicateV1(t *testing.T) {
	cases := map[string]string{
		"https://example.com":                     "https://example.com/v1/chat/completions",
		"https://example.com/v1":                  "https://example.com/v1/chat/completions",
		"https://example.com/v1/":                 "https://example.com/v1/chat/completions",
		"https://example.com/v1/chat/completions": "https://example.com/v1/chat/completions",
	}
	for input, want := range cases {
		if got := BuildOpenAIChatCompletionsURL(input); got != want {
			t.Fatalf("BuildOpenAIChatCompletionsURL(%q) = %q, want %q", input, got, want)
		}
	}
}
