package mcp

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"sync"

	"opencode-proxy/internal/webtools"
)

type Server struct {
	fetcher  *webtools.Fetcher
	searcher *webtools.Searcher
	logger   *slog.Logger
	w        io.Writer
	mu       sync.Mutex
}

func NewServer(fetcher *webtools.Fetcher, searcher *webtools.Searcher, logger *slog.Logger) *Server {
	return &Server{
		fetcher:  fetcher,
		searcher: searcher,
		logger:   logger,
		w:        os.Stdout,
	}
}

func (s *Server) Run() error {
	dec := json.NewDecoder(os.Stdin)
	for {
		var req JSONRPCRequest
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF || err == io.ErrUnexpectedEOF {
				return nil
			}
			return fmt.Errorf("JSON decode hatası: %w", err)
		}
		s.handleRequest(req)
	}
}

func (s *Server) send(resp JSONRPCResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := json.Marshal(resp)
	if err != nil {
		s.logger.Error("JSON marshal hatası", slog.String("error", err.Error()))
		return
	}
	s.w.Write(data)
	s.w.Write([]byte{'\n'})
	if f, ok := s.w.(interface{ Sync() error }); ok {
		f.Sync()
	}
}

func (s *Server) handleRequest(req JSONRPCRequest) {
	switch req.Method {
	case "initialize":
		s.handleInitialize(req)
	case "notifications/initialized":
	case "tools/list":
		s.handleToolsList(req)
	case "tools/call":
		s.handleToolsCall(req)
	default:
		s.send(JSONRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error: &RPCError{
				Code:    -32601,
				Message: fmt.Sprintf("bilinmeyen metod: %s", req.Method),
			},
		})
	}
}

func (s *Server) handleInitialize(req JSONRPCRequest) {
	s.send(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]any{
				"tools": map[string]any{},
			},
			"serverInfo": map[string]any{
				"name":    "opencode-proxy",
				"version": "1.0.0",
			},
		},
	})
}

func (s *Server) handleToolsList(req JSONRPCRequest) {
	s.send(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      req.ID,
		Result: map[string]any{
			"tools": []Tool{
				{
					Name:        "web_search",
					Description: "DuckDuckGo üzerinden web araması yap. Güncel bilgi, dokümantasyon veya herhangi bir konu için kullan.",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"query": map[string]any{
								"type":        "string",
								"description": "Arama sorgusu",
							},
							"allowed_domains": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Sadece bu domainlerden sonuçları dahil et",
							},
							"blocked_domains": map[string]any{
								"type":        "array",
								"items":       map[string]any{"type": "string"},
								"description": "Bu domainlerden sonuçları hariç tut",
							},
						},
						"required": []string{"query"},
					},
				},
				{
					Name:        "web_fetch",
					Description: "Bir URL'nin içeriğini çeker ve metin olarak döner. Web sayfalarını okumak için kullan.",
					InputSchema: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"url": map[string]any{
								"type":        "string",
								"description": "Çekilecek URL",
							},
							"prompt": map[string]any{
								"type":        "string",
								"description": "İçerikten çıkarmak istediğiniz bilgiyi açıkla (opsiyonel)",
							},
						},
						"required": []string{"url"},
					},
				},
			},
		},
	})
}

func (s *Server) handleToolsCall(req JSONRPCRequest) {
	var params ToolCallParams
	if err := parseParams(req.Params, &params); err != nil {
		s.sendToolError(req.ID, fmt.Sprintf("parametre hatası: %v", err))
		return
	}

	switch params.Name {
	case "web_search":
		s.execWebSearch(req.ID, params.Arguments)
	case "web_fetch":
		s.execWebFetch(req.ID, params.Arguments)
	default:
		s.sendToolError(req.ID, fmt.Sprintf("bilinmeyen araç: %s", params.Name))
	}
}

func (s *Server) execWebSearch(id any, args map[string]any) {
	query, _ := args["query"].(string)
	if query == "" {
		s.sendToolError(id, "query parametresi boş olamaz")
		return
	}

	var allowedDomains, blockedDomains []string
	if raw, ok := args["allowed_domains"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					allowedDomains = append(allowedDomains, s)
				}
			}
		}
	}
	if raw, ok := args["blocked_domains"]; ok {
		if arr, ok := raw.([]any); ok {
			for _, v := range arr {
				if s, ok := v.(string); ok {
					blockedDomains = append(blockedDomains, s)
				}
			}
		}
	}

	if len(allowedDomains) > 0 && len(blockedDomains) > 0 {
		s.sendToolError(id, "allowed_domains ve blocked_domains aynı anda kullanılamaz")
		return
	}

	result, err := s.searcher.SearchWithFilters(query, allowedDomains, blockedDomains)
	if err != nil {
		s.sendToolError(id, fmt.Sprintf("arama hatası: %v", err))
		return
	}

	s.sendToolResult(id, formatSearchResults(result))
}

func (s *Server) execWebFetch(id any, args map[string]any) {
	targetURL, _ := args["url"].(string)
	if targetURL == "" {
		s.sendToolError(id, "url parametresi boş olamaz")
		return
	}

	prompt, _ := args["prompt"].(string)

	result, err := s.fetcher.FetchWithPrompt(targetURL, prompt)
	if err != nil {
		s.sendToolError(id, fmt.Sprintf("fetch hatası: %v", err))
		return
	}

	var b strings.Builder
	if result.Title != "" {
		fmt.Fprintf(&b, "# %s\n\n", result.Title)
	}
	b.WriteString(result.Content)

	if result.StatusCode > 0 {
		fmt.Fprintf(&b, "\n\n---\n*Durum: %d | Boyut: %d bayt | Süre: %dms*", result.StatusCode, result.Bytes, result.DurationMs)
	}

	s.sendToolResult(id, b.String())
}

func (s *Server) sendToolResult(id any, text string) {
	s.send(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]any{
				{
					"type": "text",
					"text": text,
				},
			},
		},
	})
}

func (s *Server) sendToolError(id any, msg string) {
	s.send(JSONRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"isError": true,
			"content": []map[string]any{
				{
					"type": "text",
					"text": msg,
				},
			},
		},
	})
}

func formatSearchResults(resp *webtools.SearchResponse) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Arama sorgusu: %s\n\n", resp.Query)
	for i, r := range resp.Results {
		fmt.Fprintf(&b, "%d. %s\n   %s\n", i+1, r.Title, r.URL)
		if r.Snippet != "" {
			fmt.Fprintf(&b, "   %s\n", r.Snippet)
		}
		b.WriteByte('\n')
	}
	if len(resp.Results) == 0 {
		b.WriteString("Sonuç bulunamadı.\n")
	}
	b.WriteString("\nKAYNAKLARI ZORUNLU OLARAK YANITINIZA EKLEYİN - kaynakları markdown bağlantıları olarak listeyin.")
	return b.String()
}

func parseParams(raw json.RawMessage, target any) error {
	if raw == nil {
		return fmt.Errorf("parametreler eksik")
	}
	return json.Unmarshal(raw, target)
}