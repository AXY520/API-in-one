package handler

import (
	"api-in-one/model"
	"testing"
)

func TestApplyModelSystemPromptPrependsWhenMissing(t *testing.T) {
	req := &model.ChatCompletionRequest{
		Model: "m",
		Messages: []model.Message{{
			Role:    "user",
			Content: "hi",
		}},
	}

	applyModelSystemPrompt(req, "be concise")

	if len(req.Messages) != 2 {
		t.Fatalf("expected injected system message, got %#v", req.Messages)
	}
	if req.Messages[0].Role != "system" || req.Messages[0].Content != "be concise" {
		t.Fatalf("unexpected system message: %#v", req.Messages[0])
	}
}

func TestApplyModelSystemPromptAppendsToExistingSystem(t *testing.T) {
	req := &model.ChatCompletionRequest{
		Model: "m",
		Messages: []model.Message{{
			Role:    "system",
			Content: "existing",
		}},
	}

	applyModelSystemPrompt(req, "extra")

	if req.Messages[0].Content != "existing\n\nextra" {
		t.Fatalf("unexpected merged prompt: %#v", req.Messages[0].Content)
	}
}
