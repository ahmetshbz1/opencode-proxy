package convert

import (
	"encoding/json"
	"strings"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/openai"
)

// ToOpenAI, Anthropic formatındaki isteği OpenAI formatına dönüştürür.
func ToOpenAI(req anthropic.Request) openai.Request {
	oai := openai.Request{
		Model:       req.Model,
		MaxTokens:   req.MaxTokens,
		Stream:      req.Stream,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
	}

	oai.Messages = append(oai.Messages, convertSystem(req.System)...)

	for _, t := range req.Tools {
		oai.Tools = append(oai.Tools, openai.Tool{
			Type: "function",
			Function: openai.ToolFn{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  t.InputSchema,
			},
		})
	}

	for _, msg := range req.Messages {
		oai.Messages = append(oai.Messages, convertMessage(msg)...)
	}

	return oai
}

func convertSystem(sys json.RawMessage) []openai.Message {
	if len(sys) == 0 {
		return nil
	}
	var s string
	if err := json.Unmarshal(sys, &s); err == nil {
		return []openai.Message{{Role: "system", Content: &s}}
	}
	var blocks []anthropic.TextBlock
	if err := json.Unmarshal(sys, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			sb.WriteString(b.Text)
			sb.WriteByte('\n')
		}
		content := sb.String()
		return []openai.Message{{Role: "system", Content: &content}}
	}
	return nil
}

type blockPeek struct {
	Type string `json:"type"`
}

func convertMessage(msg anthropic.Message) []openai.Message {
	var content string
	if err := json.Unmarshal(msg.Content, &content); err == nil {
		return []openai.Message{{Role: msg.Role, Content: &content}}
	}

	var blocks []json.RawMessage
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		s := string(msg.Content)
		return []openai.Message{{Role: msg.Role, Content: &s}}
	}

	var textParts []string
	var toolUses []anthropic.ToolUseBlock
	var toolResults []anthropic.ToolResultBlock

	for _, raw := range blocks {
		var peek blockPeek
		if err := json.Unmarshal(raw, &peek); err != nil {
			continue
		}
		switch peek.Type {
		case "text":
			var tb anthropic.TextBlock
			json.Unmarshal(raw, &tb)
			textParts = append(textParts, tb.Text)
		case "tool_use":
			var tu anthropic.ToolUseBlock
			json.Unmarshal(raw, &tu)
			toolUses = append(toolUses, tu)
		case "tool_result":
			var tr anthropic.ToolResultBlock
			json.Unmarshal(raw, &tr)
			toolResults = append(toolResults, tr)
		case "image":
			textParts = append(textParts, "[image]")
		}
	}

	if len(toolResults) > 0 {
		var msgs []openai.Message
		for _, tr := range toolResults {
			content := tr.ContentText()
			msgs = append(msgs, openai.Message{
				Role:       "tool",
				Content:    &content,
				ToolCallID: tr.ToolUseID,
			})
		}
		return msgs
	}

	if len(toolUses) > 0 {
		var calls []openai.ToolCall
		for _, tu := range toolUses {
			args := string(tu.Input)
			if args == "" || args == "null" {
				args = "{}"
			}
			calls = append(calls, openai.ToolCall{
				ID:   tu.ID,
				Type: "function",
				Function: openai.FnCall{
					Name:      tu.Name,
					Arguments: args,
				},
			})
		}
		m := openai.Message{Role: "assistant", ToolCalls: &calls}
		if len(textParts) > 0 {
			s := strings.Join(textParts, "\n")
			m.Content = &s
		}
		return []openai.Message{m}
	}

	if len(textParts) > 0 {
		s := strings.Join(textParts, "\n")
		return []openai.Message{{Role: msg.Role, Content: &s}}
	}

	return nil
}
