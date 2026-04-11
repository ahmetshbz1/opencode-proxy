package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"opencode-proxy/internal/webtools"
)

func newTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestHandleInitialize(t *testing.T) {
	srv := NewServer(nil, nil, newTestLogger())

	var buf bytes.Buffer
	srv.w = &buf
	srv.handleRequest(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("hata: %s", resp.Error.Message)
	}

	result := resp.Result.(map[string]any)
	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("protocolVersion = %v", result["protocolVersion"])
	}
	info := result["serverInfo"].(map[string]any)
	if info["name"] != "opencode-proxy" {
		t.Errorf("name = %v", info["name"])
	}
}

func TestHandleToolsList(t *testing.T) {
	srv := NewServer(nil, nil, newTestLogger())

	var buf bytes.Buffer
	srv.w = &buf
	srv.handleRequest(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      2,
		Method:  "tools/list",
	})

	var resp JSONRPCResponse
	if err := json.Unmarshal(buf.Bytes(), &resp); err != nil {
		t.Fatalf("JSON parse: %v", err)
	}

	result := resp.Result.(map[string]any)
	toolsRaw, _ := json.Marshal(result["tools"])
	var tools []Tool
	json.Unmarshal(toolsRaw, &tools)

	if len(tools) != 2 {
		t.Fatalf("tools = %d, want 2", len(tools))
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
	}
	if !names["web_search"] || !names["web_fetch"] {
		t.Error("beklenen araçlar yok")
	}
}

func TestHandleToolsCallWebSearchEmptyQuery(t *testing.T) {
	srv := NewServer(nil, nil, newTestLogger())

	params, _ := json.Marshal(ToolCallParams{
		Name:      "web_search",
		Arguments: map[string]any{"query": ""},
	})

	var buf bytes.Buffer
	srv.w = &buf
	srv.handleRequest(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      3,
		Method:  "tools/call",
		Params:  params,
	})

	var resp JSONRPCResponse
	json.Unmarshal(buf.Bytes(), &resp)

	result := resp.Result.(map[string]any)
	isError, _ := result["isError"].(bool)
	if !isError {
		t.Error("boş query isError=true olmalı")
	}
}

func TestHandleToolsCallWebFetch(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Test</title></head><body><p>merhaba</p></body></html>`))
	}))
	defer upstream.Close()

	srv := NewServer(webtools.NewFetcher(newTestLogger()), nil, newTestLogger())

	params, _ := json.Marshal(ToolCallParams{
		Name:      "web_fetch",
		Arguments: map[string]any{"url": upstream.URL},
	})

	var buf bytes.Buffer
	srv.w = &buf
	srv.handleRequest(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      4,
		Method:  "tools/call",
		Params:  params,
	})

	var resp JSONRPCResponse
	json.Unmarshal(buf.Bytes(), &resp)

	if resp.Error != nil {
		t.Fatalf("hata: %s", resp.Error.Message)
	}

	result := resp.Result.(map[string]any)
	content := result["content"].([]any)
	textBlock := content[0].(map[string]any)
	text := textBlock["text"].(string)

	if !strings.Contains(text, "Test") {
		t.Errorf("title yok: %s", text)
	}
	if !strings.Contains(text, "merhaba") {
		t.Errorf("içerik yok: %s", text)
	}
}

func TestHandleToolsCallUnknownTool(t *testing.T) {
	srv := NewServer(nil, nil, newTestLogger())

	params, _ := json.Marshal(ToolCallParams{Name: "unknown_tool"})

	var buf bytes.Buffer
	srv.w = &buf
	srv.handleRequest(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      5,
		Method:  "tools/call",
		Params:  params,
	})

	var resp JSONRPCResponse
	json.Unmarshal(buf.Bytes(), &resp)

	result := resp.Result.(map[string]any)
	isError, _ := result["isError"].(bool)
	if !isError {
		t.Error("bilinmeyen araç isError=true olmalı")
	}
}

func TestHandleUnknownMethod(t *testing.T) {
	srv := NewServer(nil, nil, newTestLogger())

	var buf bytes.Buffer
	srv.w = &buf
	srv.handleRequest(JSONRPCRequest{
		JSONRPC: "2.0",
		ID:      6,
		Method:  "foo/bar",
	})

	var resp JSONRPCResponse
	json.Unmarshal(buf.Bytes(), &resp)

	if resp.Error == nil {
		t.Fatal("hata bekleniyordu")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("code = %d, want -32601", resp.Error.Code)
	}
}

func TestFormatSearchResults(t *testing.T) {
	resp := &webtools.SearchResponse{
		Query: "test",
		Results: []webtools.SearchResult{
			{Title: "Başlık 1", URL: "https://example.com", Snippet: "Açıklama"},
			{Title: "Başlık 2", URL: "https://test.com"},
		},
	}

	text := formatSearchResults(resp)
	if !strings.Contains(text, "Başlık 1") {
		t.Error("Başlık 1 yok")
	}
	if !strings.Contains(text, "Açıklama") {
		t.Error("Açıklama yok")
	}
}

func TestFormatSearchResultsEmpty(t *testing.T) {
	text := formatSearchResults(&webtools.SearchResponse{Query: "yok"})
	if !strings.Contains(text, "Sonuç bulunamadı") {
		t.Error("boş sonuç mesajı yok")
	}
}