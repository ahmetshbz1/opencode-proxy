package webtools

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/url"
)

func HandleFetch(fetcher *Fetcher, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var targetURL, prompt string

		if r.Method == http.MethodGet {
			targetURL = r.URL.Query().Get("url")
			prompt = r.URL.Query().Get("prompt")
		} else {
			var body struct {
				URL    string `json:"url"`
				Prompt string `json:"prompt"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "geçersiz istek", http.StatusBadRequest)
				return
			}
			targetURL = body.URL
			prompt = body.Prompt
		}

		if targetURL == "" {
			http.Error(w, "url boş olamaz", http.StatusBadRequest)
			return
		}

		parsed, err := url.Parse(targetURL)
		if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			http.Error(w, "geçersiz URL", http.StatusBadRequest)
			return
		}

		result, err := fetcher.FetchWithPrompt(targetURL, prompt)
		if err != nil {
			logger.Error("fetch hatası", slog.String("error", err.Error()), slog.String("url", targetURL))
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(result)
	}
}