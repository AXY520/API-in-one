package handler

import (
	"api-in-one/config"
	"api-in-one/model"
	"strings"
)

func systemPromptForModel(requestedModel, resolvedModel string) string {
	prompts := config.GetModelSystemPrompts()
	if prompt := strings.TrimSpace(prompts[requestedModel]); prompt != "" {
		return prompt
	}
	if resolvedModel != "" && resolvedModel != requestedModel {
		return strings.TrimSpace(prompts[resolvedModel])
	}
	return ""
}

func applyModelSystemPrompt(req *model.ChatCompletionRequest, prompt string) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return
	}
	if len(req.Messages) == 0 {
		req.Messages = append(req.Messages, model.Message{Role: "system", Content: prompt})
		return
	}
	for i := range req.Messages {
		if req.Messages[i].Role == "system" || req.Messages[i].Role == "developer" {
			existing := strings.TrimSpace(contentToString(req.Messages[i].Content))
			if existing == "" {
				req.Messages[i].Content = prompt
			} else if !strings.Contains(existing, prompt) {
				req.Messages[i].Content = existing + "\n\n" + prompt
			}
			if req.Messages[i].Role == "developer" {
				req.Messages[i].Role = "system"
			}
			return
		}
	}
	req.Messages = append([]model.Message{{Role: "system", Content: prompt}}, req.Messages...)
}
