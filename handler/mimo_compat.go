package handler

import (
	"api-in-one/model"
	"api-in-one/relay/adaptor"
	"fmt"
	"strings"
)

func isMiMoCompatResult(channelName, requestedModel, resolvedModel string) bool {
	return isMiMoCompatModel(requestedModel) || isMiMoCompatModel(resolvedModel) || isXiaomiChannel(channelName)
}

func isMiMoCompatChannelResult(ch *model.Channel, requestedModel, resolvedModel string) bool {
	if ch != nil && ch.DisableMiMoCompat {
		return false
	}
	channelName := ""
	if ch != nil {
		channelName = ch.Name
	}
	return isMiMoCompatResult(channelName, requestedModel, resolvedModel)
}

func isMiMoCompatModel(modelName string) bool {
	return strings.Contains(strings.ToLower(modelName), "mimo")
}

func isXiaomiChannel(channelName string) bool {
	name := strings.ToLower(channelName)
	return strings.Contains(name, "xiaomi") || strings.Contains(channelName, "小米")
}

// mergeConsecutiveRoles applies the MiMo/Codex compatibility rule that avoids
// consecutive non-system roles in chat history.
func mergeConsecutiveRoles(msgs []model.Message) []model.Message {
	if len(msgs) <= 1 {
		return msgs
	}
	var merged []model.Message
	for _, msg := range msgs {
		if len(merged) > 0 && merged[len(merged)-1].Role == msg.Role && msg.Role != "system" {
			prev := &merged[len(merged)-1]
			prevText := contentToString(prev.Content)
			curText := contentToString(msg.Content)
			if prevText != "" && curText != "" {
				prev.Content = prevText + "\n" + curText
			} else if curText != "" {
				prev.Content = curText
			}
			prev.ToolCalls = append(prev.ToolCalls, msg.ToolCalls...)
			if msg.ReasoningContent != "" {
				prev.ReasoningContent += "\n" + msg.ReasoningContent
			}
		} else {
			merged = append(merged, msg)
		}
	}
	return merged
}

type fakeToolCall = adaptor.FakeToolCall

// cleanResponseToolCalls removes MiMo's XML-like fake tool call syntax from
// Claude-format text responses after they have been converted into real calls.
func cleanResponseToolCalls(resp *model.ChatCompletionResponse) {
	adaptor.CleanResponseToolCalls(resp)
}

func extractFakeToolCalls(text string) (string, []fakeToolCall) {
	return adaptor.ExtractFakeToolCalls(text)
}

func removeFakeToolCalls(text string) string {
	return adaptor.RemoveFakeToolCalls(text)
}


func contentToString(content interface{}) string {
	if content == nil {
		return ""
	}
	if s, ok := content.(string); ok {
		return s
	}
	if parts, ok := content.([]model.ContentPart); ok {
		var texts []string
		for _, p := range parts {
			if p.Type == "text" && p.Text != "" {
				texts = append(texts, p.Text)
			}
		}
		return strings.Join(texts, "")
	}
	return fmt.Sprintf("%v", content)
}
