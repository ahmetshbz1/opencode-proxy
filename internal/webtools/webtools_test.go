package webtools

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestWebLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestFetchInternal(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<!DOCTYPE html><html><head><title>Test Sayfa</title></head><body><p>Merhaba dünya</p></body></html>`))
	}))
	defer upstream.Close()

	fetcher := NewFetcher(newTestWebLogger())
	result, err := fetcher.Fetch(upstream.URL)

	if err != nil {
		t.Fatalf("Fetch hatası: %v", err)
	}
	if result.Title != "Test Sayfa" {
		t.Errorf("title = %q, want %q", result.Title, "Test Sayfa")
	}
	if !strings.Contains(result.Content, "Merhaba dünya") {
		t.Errorf("content = %q, 'Merhaba dünya' içermiyor", result.Content)
	}
}

func TestFetchPlainText(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("düz metin içeriği"))
	}))
	defer upstream.Close()

	fetcher := NewFetcher(newTestWebLogger())
	result, err := fetcher.Fetch(upstream.URL)

	if err != nil {
		t.Fatalf("Fetch hatası: %v", err)
	}
	if result.Title != "" {
		t.Errorf("title = %q, want boş", result.Title)
	}
}

func TestFetchInvalidURL(t *testing.T) {
	fetcher := NewFetcher(newTestWebLogger())
	_, err := fetcher.Fetch("ftp://invalid")

	if err == nil {
		t.Fatal("beklenen hata yok")
	}
}

func TestFetchNonHTML(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/pdf")
		w.Write([]byte("binary data"))
	}))
	defer upstream.Close()

	fetcher := NewFetcher(newTestWebLogger())
	_, err := fetcher.Fetch(upstream.URL)

	// PDF artık desteklenmiyor — Fetch sadece HTML/text/json kabul eder
	if err == nil {
		t.Fatal("beklenen hata yok")
	}
}

func TestHandleFetchEndpoint(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>Endpoint Test</title></head><body><p>içerik</p></body></html>`))
	}))
	defer upstream.Close()

	fetcher := NewFetcher(newTestWebLogger())
	handler := HandleFetch(fetcher, newTestWebLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/tools/web_fetch?url="+upstream.URL, nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}

	var result FetchResult
	if err := json.NewDecoder(w.Body).Decode(&result); err != nil {
		t.Fatalf("JSON decode hatası: %v", err)
	}
	if result.Title != "Endpoint Test" {
		t.Errorf("title = %q, want %q", result.Title, "Endpoint Test")
	}
}

func TestHandleFetchMissingURL(t *testing.T) {
	fetcher := NewFetcher(newTestWebLogger())
	handler := HandleFetch(fetcher, newTestWebLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/tools/web_fetch", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleFetchPost(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(`<html><head><title>POST Test</title></head><body>ok</body></html>`))
	}))
	defer upstream.Close()

	fetcher := NewFetcher(newTestWebLogger())
	handler := HandleFetch(fetcher, newTestWebLogger())

	body := `{"url":"` + upstream.URL + `"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/tools/web_fetch", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", w.Code, http.StatusOK)
	}
}

func TestHandleSearchMissingQuery(t *testing.T) {
	searcher := NewSearcher(newTestWebLogger())
	handler := HandleSearch(searcher, newTestWebLogger())

	req := httptest.NewRequest(http.MethodGet, "/v1/tools/web_search", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", w.Code, http.StatusBadRequest)
	}
}

func TestHandleSearchMethodNotAllowed(t *testing.T) {
	searcher := NewSearcher(newTestWebLogger())
	handler := HandleSearch(searcher, newTestWebLogger())

	req := httptest.NewRequest(http.MethodDelete, "/v1/tools/web_search", nil)
	w := httptest.NewRecorder()
	handler(w, req)

	if w.Code != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
	}
}

func TestExtractTextScriptsRemoved(t *testing.T) {
	htmlData := `<!DOCTYPE html><html><head><title>Başlık</title><script>var x = 1;</script></head><body><p>İçerik</p><script>console.log("sil")</script></body></html>`
	title, content := fallbackExtract([]byte(htmlData))

	if title != "Başlık" {
		t.Errorf("title = %q, want %q", title, "Başlık")
	}
	if strings.Contains(content, "console.log") || strings.Contains(content, "var x") {
		t.Errorf("content script içeriyor: %q", content)
	}
	if !strings.Contains(content, "İçerik") {
		t.Errorf("content 'İçerik' içermiyor: %q", content)
	}
}
