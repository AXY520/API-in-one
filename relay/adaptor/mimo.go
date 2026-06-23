package adaptor

import (
	"api-in-one/model"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

type FakeToolCall struct {
	Name      string
	Arguments string
}

var fakeToolCallBlockRe = regexp.MustCompile(`(?s)<tool_call>\s*<function=([A-Za-z0-9_.-]+)>\s*(.*?)</function>\s*</tool_call>`)

// ExtractFakeToolCalls parses XML-like tool calls out of raw text.
func ExtractFakeToolCalls(text string) (string, []FakeToolCall) {
	if text == "" {
		return "", nil
	}
	var calls []FakeToolCall
	cleaned := fakeToolCallBlockRe.ReplaceAllStringFunc(text, func(block string) string {
		matches := fakeToolCallBlockRe.FindStringSubmatch(block)
		if len(matches) != 3 {
			return block
		}
		name := strings.TrimSpace(matches[1])
		args := extractFakeToolArguments(matches[2])
		if name != "" {
			calls = append(calls, FakeToolCall{Name: name, Arguments: args})
		}
		return ""
	})
	return trimFakeToolResidue(cleaned), calls
}

func extractFakeToolArguments(body string) string {
	body = strings.TrimSpace(body)
	params := parseFakeToolParameters(body)
	if len(params) > 0 {
		args := map[string]interface{}{}
		for i, param := range params {
			name := strings.TrimSpace(param.name)
			valueText := strings.TrimSpace(param.value)
			if name == "" {
				name = fmt.Sprintf("arg%d", i+1)
			}
			if strings.HasPrefix(name, "[") || strings.HasPrefix(name, "{") {
				valueText = name
				name = "plan"
			}
			args[name] = decodeFakeToolValue(valueText)
		}
		if data, err := json.Marshal(args); err == nil {
			return string(data)
		}
	}
	if body == "" {
		return "{}"
	}
	if json.Valid([]byte(body)) {
		return body
	}
	encoded, err := json.Marshal(map[string]string{"input": body})
	if err != nil {
		return "{}"
	}
	return string(encoded)
}

type fakeToolParam struct {
	name  string
	value string
}

func parseFakeToolParameters(body string) []fakeToolParam {
	var params []fakeToolParam
	for {
		start := strings.Index(body, "<parameter")
		if start == -1 {
			break
		}
		body = body[start+len("<parameter"):]
		end := strings.Index(body, "</parameter>")
		if end == -1 {
			break
		}
		raw := strings.TrimSpace(body[:end])
		body = body[end+len("</parameter>"):]

		var param fakeToolParam
		if strings.HasPrefix(raw, "=") {
			raw = strings.TrimSpace(raw[1:])
			if split := strings.Index(raw, ">"); split != -1 {
				param.name = strings.TrimSpace(raw[:split])
				param.value = strings.TrimSpace(raw[split+1:])
			} else if strings.HasPrefix(raw, "[") || strings.HasPrefix(raw, "{") {
				param.name = raw
				param.value = raw
			} else {
				param.value = raw
			}
		} else if strings.HasPrefix(raw, ">") {
			param.value = strings.TrimSpace(raw[1:])
		} else {
			param.value = raw
		}
		params = append(params, param)
	}
	return params
}

func decodeFakeToolValue(value string) interface{} {
	if value == "" {
		return ""
	}
	var decoded interface{}
	if json.Unmarshal([]byte(value), &decoded) == nil {
		return decoded
	}
	return value
}

func trimFakeToolResidue(text string) string {
	text = strings.TrimSpace(text)
	text = strings.Trim(text, "•*- \t\r\n")
	return strings.TrimSpace(text)
}

// RemoveFakeToolCalls strips fake XML-like tool calls from the text.
func RemoveFakeToolCalls(text string) string {
	if cleaned, calls := ExtractFakeToolCalls(text); len(calls) > 0 {
		return cleaned
	}
	prefixes := []string{"<function="}
	closings := []string{"</function"}
	for i, prefix := range prefixes {
		closing := closings[i]
		for {
			idx := strings.Index(text, prefix)
			if idx == -1 {
				break
			}
			rest := text[idx:]
			endIdx := strings.Index(rest, closing)
			if endIdx != -1 {
				end := endIdx + len(closing)
				if end < len(rest) && rest[end] == '>' {
					end++
				}
				text = strings.TrimSpace(text[:idx] + rest[end:])
			} else {
				text = strings.TrimSpace(text[:idx])
				break
			}
		}
	}
	return text
}

// CleanResponseToolCalls removes MiMo's XML-like fake tool call syntax from
// Claude-format text responses after they have been converted into real calls.
func CleanResponseToolCalls(resp *model.ChatCompletionResponse) {
	if len(resp.Choices) == 0 || resp.Choices[0].Message == nil {
		return
	}
	msg := resp.Choices[0].Message
	if msg.Content == nil {
		return
	}
	text, ok := msg.Content.(string)
	if !ok || text == "" {
		return
	}
	cleaned := RemoveFakeToolCalls(text)
	if cleaned != text {
		msg.Content = strings.TrimSpace(cleaned)
	}
}
