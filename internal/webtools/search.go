package webtools

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

type SearchResponse struct {
	Query         string         `json:"query"`
	Results       []SearchResult `json:"results"`
	DurationMs    int64          `json:"duration_ms"`
	ResultCount   int            `json:"result_count"`
}

type Searcher struct {
	client *http.Client
	logger *slog.Logger
}

func NewSearcher(logger *slog.Logger) *Searcher {
	return &Searcher{
		client: &http.Client{
			Timeout: 15 * time.Second,
			Transport: &http.Transport{
				MaxIdleConns:        10,
				IdleConnTimeout:     60 * time.Second,
				MaxIdleConnsPerHost: 5,
			},
		},
		logger: logger,
	}
}

func (s *Searcher) Search(query string) (*SearchResponse, error) {
	return s.SearchWithFilters(query, nil, nil)
}

func (s *Searcher) SearchWithFilters(query string, allowedDomains []string, blockedDomains []string) (*SearchResponse, error) {
	start := time.Now()
	s.logger.Debug("web search", slog.String("query", query))

	searchURL := fmt.Sprintf("https://lite.duckduckgo.com/lite/?q=%s", url.QueryEscape(query))

	req, err := http.NewRequest(http.MethodGet, searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("istek oluşturulamadı: %w", err)
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("arama başarısız: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("arama HTTP %d döndü", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("yanıt okunamadı: %w", err)
	}

	results := parseDuckDuckGoLite(body)

	filtered := applyDomainFilters(results, allowedDomains, blockedDomains)

	const maxResults = 10
	if len(filtered) > maxResults {
		filtered = filtered[:maxResults]
	}

	return &SearchResponse{
		Query:       query,
		Results:     filtered,
		DurationMs:  time.Since(start).Milliseconds(),
		ResultCount: len(filtered),
	}, nil
}

func applyDomainFilters(results []SearchResult, allowedDomains []string, blockedDomains []string) []SearchResult {
	if len(allowedDomains) == 0 && len(blockedDomains) == 0 {
		return results
	}

	allowedSet := make(map[string]bool, len(allowedDomains))
	for _, d := range allowedDomains {
		allowedSet[strings.ToLower(d)] = true
	}
	blockedSet := make(map[string]bool, len(blockedDomains))
	for _, d := range blockedDomains {
		blockedSet[strings.ToLower(d)] = true
	}

	var filtered []SearchResult
	for _, r := range results {
		u, err := url.Parse(r.URL)
		if err != nil {
			continue
		}
		host := strings.ToLower(u.Hostname())

		if len(blockedSet) > 0 && blockedSet[host] {
			continue
		}

		if len(allowedSet) > 0 {
			found := false
			for domain := range allowedSet {
				if host == domain || strings.HasSuffix(host, "."+domain) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}

		filtered = append(filtered, r)
	}
	return filtered
}

func extractUDDG(href string) string {
	if !strings.Contains(href, "uddg=") {
		return ""
	}
	u, err := url.Parse(href)
	if err != nil {
		return ""
	}
	if strings.HasPrefix(href, "//") {
		u, err = url.Parse("https:" + href)
		if err != nil {
			return ""
		}
	}
	decoded := u.Query().Get("uddg")
	if decoded == "" {
		return ""
	}
	if !strings.HasPrefix(decoded, "http") {
		return ""
	}
	return decoded
}

func parseDuckDuckGoLite(data []byte) []SearchResult {
	doc, err := html.Parse(strings.NewReader(string(data)))
	if err != nil {
		return nil
	}

	var results []SearchResult
	var currentTitle strings.Builder
	var currentURL string
	inResultLink := false
	inSnippet := false
	var snippetBuf strings.Builder

	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "a" {
			isResultLink := false
			href := ""
			for _, attr := range n.Attr {
				if attr.Key == "class" && attr.Val == "result-link" {
					isResultLink = true
				}
				if attr.Key == "href" {
					href = attr.Val
				}
			}

			if isResultLink && href != "" {
				inResultLink = true
				currentTitle.Reset()
				currentURL = ""

				extracted := extractUDDG(href)
				if extracted != "" {
					currentURL = extracted
				}
			}
		}

		if n.Type == html.ElementNode && n.Data == "td" {
			for _, attr := range n.Attr {
				if attr.Key == "class" && attr.Val == "result-snippet" {
					inSnippet = true
					snippetBuf.Reset()
				}
			}
		}

		if n.Type == html.TextNode {
			text := strings.TrimSpace(n.Data)
			if text == "" {
				goto children
			}
			if inResultLink {
				currentTitle.WriteString(text)
			}
			if inSnippet {
				snippetBuf.WriteString(text)
				snippetBuf.WriteByte(' ')
			}
		}

	children:
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}

		if n.Type == html.ElementNode && n.Data == "a" && inResultLink {
			title := strings.TrimSpace(currentTitle.String())
			if title != "" && currentURL != "" {
				results = append(results, SearchResult{
					Title: title,
					URL:   currentURL,
				})
			}
			inResultLink = false
			currentTitle.Reset()
			currentURL = ""
		}

		if n.Type == html.ElementNode && n.Data == "td" && inSnippet {
			snippet := strings.TrimSpace(snippetBuf.String())
			if len(results) > 0 && snippet != "" {
				results[len(results)-1].Snippet = snippet
			}
			inSnippet = false
		}
	}

	walk(doc)

	const maxResults = 10
	if len(results) > maxResults {
		results = results[:maxResults]
	}

	return results
}

func HandleSearch(searcher *Searcher, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost && r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var query string
		var allowedDomains []string
		var blockedDomains []string

		if r.Method == http.MethodGet {
			query = r.URL.Query().Get("q")
		} else {
			var body struct {
				Query          string   `json:"query"`
				AllowedDomains []string `json:"allowed_domains"`
				BlockedDomains []string `json:"blocked_domains"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "geçersiz istek", http.StatusBadRequest)
				return
			}
			query = body.Query
			allowedDomains = body.AllowedDomains
			blockedDomains = body.BlockedDomains

			if len(allowedDomains) > 0 && len(blockedDomains) > 0 {
				http.Error(w, "allowed_domains ve blocked_domains aynı anda kullanılamaz", http.StatusBadRequest)
				return
			}
		}

		if query == "" {
			http.Error(w, "sorgu boş olamaz", http.StatusBadRequest)
			return
		}

		result, err := searcher.SearchWithFilters(query, allowedDomains, blockedDomains)
		if err != nil {
			logger.Error("arama hatası", slog.String("error", err.Error()))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}