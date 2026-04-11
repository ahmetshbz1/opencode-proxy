package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestRunCodexAuthPersistsProviderToConfig(t *testing.T) {
	tokenExchangeCalled := false

	deviceServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/usercode":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"device_auth_id":"device-auth-1","user_code":"ABCD-EFGH","interval":0}`))
		case "/token":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"authorization_code":"auth-code-1","code_verifier":"verifier-1","code_challenge":"challenge-1"}`))
		default:
			t.Fatalf("beklenmeyen device path: %s", r.URL.Path)
		}
	}))
	defer deviceServer.Close()

	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenExchangeCalled = true
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm hatası: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("code"); got != "auth-code-1" {
			t.Fatalf("code = %q, want auth-code-1", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-1","refresh_token":"refresh-1","id_token":"id-1","expires_in":3600}`))
	}))
	defer oauthServer.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"port":8787,"providers":[{"name":"z.ai","type":"anthropic","base_url":"https://example.com","api_key":"k","priority":1}]}`), 0644); err != nil {
		t.Fatalf("WriteFile hatası: %v", err)
	}

	var stdout bytes.Buffer
	err := RunCodexAuth(context.Background(), CodexAuthOptions{
		ConfigPath:      configPath,
		Name:            "codex-oauth",
		BaseURL:         "https://chatgpt.com/backend-api/codex",
		NoBrowser:       true,
		HTTPClient:      http.DefaultClient,
		Stdout:          &stdout,
		UserCodeURL:     deviceServer.URL + "/usercode",
		DeviceTokenURL:  deviceServer.URL + "/token",
		TokenURL:        oauthServer.URL,
		VerificationURL: "https://auth.openai.com/codex/device",
	})
	if err != nil {
		t.Fatalf("RunCodexAuth hatası: %v", err)
	}
	if !tokenExchangeCalled {
		t.Fatal("token exchange çağrılmadı")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile hatası: %v", err)
	}

	var cfg struct {
		Port      int `json:"port"`
		Providers []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			BaseURL  string `json:"base_url"`
			Priority int    `json:"priority"`
			OAuth    struct {
				AccessToken  string `json:"access_token"`
				RefreshToken string `json:"refresh_token"`
				IDToken      string `json:"id_token"`
				ExpiresAt    string `json:"expires_at"`
			} `json:"oauth"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("json.Unmarshal hatası: %v", err)
	}
	if len(cfg.Providers) != 2 {
		t.Fatalf("providers = %d, want 2", len(cfg.Providers))
	}

	added := cfg.Providers[1]
	if added.Name != "codex-oauth" {
		t.Fatalf("name = %q, want codex-oauth", added.Name)
	}
	if added.Type != "codex" {
		t.Fatalf("type = %q, want codex", added.Type)
	}
	if added.OAuth.RefreshToken != "refresh-1" {
		t.Fatalf("refresh_token = %q, want refresh-1", added.OAuth.RefreshToken)
	}
	if added.OAuth.AccessToken != "access-1" {
		t.Fatalf("access_token = %q, want access-1", added.OAuth.AccessToken)
	}
	if added.Priority != 2 {
		t.Fatalf("priority = %d, want 2", added.Priority)
	}
	if !strings.Contains(stdout.String(), "ABCD-EFGH") {
		t.Fatalf("stdout user code içermiyor: %s", stdout.String())
	}
}

func TestRunCodexAuthBrowserFlowPersistsProviderToConfig(t *testing.T) {
	tokenExchangeCalled := false
	callbackPort := freePort(t)

	oauthServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenExchangeCalled = true
		if err := r.ParseForm(); err != nil {
			t.Fatalf("ParseForm hatası: %v", err)
		}
		if got := r.Form.Get("grant_type"); got != "authorization_code" {
			t.Fatalf("grant_type = %q, want authorization_code", got)
		}
		if got := r.Form.Get("code"); got != "browser-code-1" {
			t.Fatalf("code = %q, want browser-code-1", got)
		}
		if got := r.Form.Get("code_verifier"); got == "" {
			t.Fatal("code_verifier boş geldi")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"browser-access","refresh_token":"browser-refresh","id_token":"browser-id","expires_in":3600}`))
	}))
	defer oauthServer.Close()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"port":8787,"providers":[]}`), 0644); err != nil {
		t.Fatalf("WriteFile hatası: %v", err)
	}

	var openedURL string
	openBrowser := func(rawURL string) error {
		openedURL = rawURL
		parsed, err := url.Parse(rawURL)
		if err != nil {
			return err
		}
		callbackURL := parsed.Query().Get("redirect_uri")
		state := parsed.Query().Get("state")
		if callbackURL == "" || state == "" {
			t.Fatalf("redirect_uri/state yok: %s", rawURL)
		}
		wantCallbackURL := "http://localhost:" + callbackPort + "/auth/callback"
		if callbackURL != wantCallbackURL {
			t.Fatalf("redirect_uri = %q, want %q", callbackURL, wantCallbackURL)
		}

		resp, err := http.Get(callbackURL + "?code=browser-code-1&state=" + url.QueryEscape(state))
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.ReadAll(resp.Body)
		return nil
	}

	var stdout bytes.Buffer
	err := RunCodexAuth(context.Background(), CodexAuthOptions{
		ConfigPath:   configPath,
		Name:         "codex-browser",
		BaseURL:      "https://chatgpt.com/backend-api/codex",
		HTTPClient:   http.DefaultClient,
		Stdout:       &stdout,
		TokenURL:     oauthServer.URL,
		AuthURL:      "https://auth.openai.com/oauth/authorize",
		CallbackPort: mustAtoi(t, callbackPort),
		OpenBrowser:  openBrowser,
	})
	if err != nil {
		t.Fatalf("RunCodexAuth hatası: %v", err)
	}
	if !tokenExchangeCalled {
		t.Fatal("browser token exchange çağrılmadı")
	}
	if openedURL == "" {
		t.Fatal("browser URL açılmadı")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("ReadFile hatası: %v", err)
	}

	if !strings.Contains(string(data), `"name": "codex-browser"`) {
		t.Fatalf("config provider içermiyor: %s", string(data))
	}
	if !strings.Contains(string(data), `"refresh_token": "browser-refresh"`) {
		t.Fatalf("config refresh_token içermiyor: %s", string(data))
	}
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("free port alınamadı: %v", err)
	}
	defer ln.Close()
	_, port, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("port parse edilemedi: %v", err)
	}
	return port
}

func mustAtoi(t *testing.T, raw string) int {
	t.Helper()
	v, err := strconv.Atoi(raw)
	if err != nil {
		t.Fatalf("atoi hatası: %v", err)
	}
	return v
}
