package convert

import (
	"encoding/json"
	"testing"

	"opencode-proxy/internal/anthropic"
)

func TestToOpenAISystemString(t *testing.T) {
	sys := json.RawMessage(`"sen bir asistansın"`)
	req := anthropic.Request{
		Model:     "test",
		MaxTokens: 100,
		System:    sys,
		Messages: []anthropic.Message{
			{Role: "user", Content: json.RawMessage(`"merhaba"`)},
		},
	}

	oai := ToOpenAI(req)

	if len(oai.Messages) != 2 {
		t.Fatalf("messages count = %d, want 2", len(oai.Messages))
	}
	if oai.Messages[0].Role != "system" {
		t.Errorf("ilk mesaj rolü = %q, want %q", oai.Messages[0].Role, "system")
	}
	if oai.Messages[0].Content == nil || *oai.Messages[0].Content != "sen bir asistansın" {
		t.Errorf("system content = %v", oai.Messages[0].Content)
	}
}

func TestToOpenAISystemBlocks(t *testing.T) {
	blocks := []anthropic.TextBlock{
		{Type: "text", Text: "blok1"},
		{Type: "text", Text: "blok2"},
	}
	sysData, _ := json.Marshal(blocks)
	req := anthropic.Request{
		Model:     "test",
		MaxTokens: 100,
		System:    sysData,
		Messages:  []anthropic.Message{},
	}

	oai := ToOpenAI(req)

	if len(oai.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(oai.Messages))
	}
	if oai.Messages[0].Role != "system" {
		t.Errorf("rol = %q, want %q", oai.Messages[0].Role, "system")
	}
}

func TestToOpenAIToolResult(t *testing.T) {
	content := []json.RawMessage{
		[]byte(`{"type":"tool_result","tool_use_id":"tu-1","content":"sonuç"}`),
	}
	contentData, _ := json.Marshal(content)
	req := anthropic.Request{
		Model:     "test",
		MaxTokens: 100,
		Messages: []anthropic.Message{
			{Role: "user", Content: contentData},
		},
	}

	oai := ToOpenAI(req)

	if len(oai.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(oai.Messages))
	}
	if oai.Messages[0].Role != "tool" {
		t.Errorf("rol = %q, want %q", oai.Messages[0].Role, "tool")
	}
	if oai.Messages[0].ToolCallID != "tu-1" {
		t.Errorf("tool_call_id = %q, want %q", oai.Messages[0].ToolCallID, "tu-1")
	}
}

func TestToOpenAIToolUse(t *testing.T) {
	content := []json.RawMessage{
		[]byte(`{"type":"tool_use","id":"tu-1","name":"read_file","input":{"path":"/test"}}`),
	}
	contentData, _ := json.Marshal(content)
	req := anthropic.Request{
		Model:     "test",
		MaxTokens: 100,
		Messages: []anthropic.Message{
			{Role: "assistant", Content: contentData},
		},
	}

	oai := ToOpenAI(req)

	if len(oai.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(oai.Messages))
	}
	if oai.Messages[0].Role != "assistant" {
		t.Errorf("rol = %q, want %q", oai.Messages[0].Role, "assistant")
	}
	if oai.Messages[0].ToolCalls == nil {
		t.Fatal("tool_calls nil")
	}
	calls := *oai.Messages[0].ToolCalls
	if len(calls) != 1 {
		t.Fatalf("tool_calls count = %d, want 1", len(calls))
	}
	if calls[0].Function.Name != "read_file" {
		t.Errorf("tool name = %q, want %q", calls[0].Function.Name, "read_file")
	}
}

func TestToOpenAITools(t *testing.T) {
	req := anthropic.Request{
		Model:     "test",
		MaxTokens: 100,
		Tools: []anthropic.Tool{
			{
				Name:        "search",
				Description: "web araması",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
			},
		},
	}

	oai := ToOpenAI(req)

	if len(oai.Tools) != 1 {
		t.Fatalf("tools count = %d, want 1", len(oai.Tools))
	}
	if oai.Tools[0].Type != "function" {
		t.Errorf("tool type = %q, want %q", oai.Tools[0].Type, "function")
	}
	if oai.Tools[0].Function.Name != "search" {
		t.Errorf("function name = %q, want %q", oai.Tools[0].Function.Name, "search")
	}
}

func TestToOpenAIImageBlock(t *testing.T) {
	content := []json.RawMessage{
		[]byte(`{"type":"image","source":{"type":"base64","data":"..."}}`),
	}
	contentData, _ := json.Marshal(content)
	req := anthropic.Request{
		Model:     "test",
		MaxTokens: 100,
		Messages: []anthropic.Message{
			{Role: "user", Content: contentData},
		},
	}

	oai := ToOpenAI(req)

	if len(oai.Messages) != 1 {
		t.Fatalf("messages count = %d, want 1", len(oai.Messages))
	}
	if oai.Messages[0].Content == nil || *oai.Messages[0].Content != "[image]" {
		t.Errorf("image placeholder = %v", oai.Messages[0].Content)
	}
}
