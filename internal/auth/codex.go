package auth

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"opencode-proxy/internal/config"
)

const (
	codexClientID                     = "app_EMoamEEZ73f0CkXaXp7hrann"
	defaultCodexBaseURL               = "https://chatgpt.com/backend-api/codex"
	defaultCodexAuthURL               = "https://auth.openai.com/oauth/authorize"
	defaultCodexVerificationURL       = "https://auth.openai.com/codex/device"
	defaultCodexUserCodeURL           = "https://auth.openai.com/api/accounts/deviceauth/usercode"
	defaultCodexDeviceTokenURL        = "https://auth.openai.com/api/accounts/deviceauth/token"
	defaultCodexOAuthTokenURL         = "https://auth.openai.com/oauth/token"
	defaultCodexTokenExchangeRedirect = "https://auth.openai.com/deviceauth/callback"
	defaultCodexCallbackPort          = 1455
)

type CodexAuthOptions struct {
	ConfigPath      string
	Name            string
	BaseURL         string
	AuthURL         string
	NoBrowser       bool
	CallbackPort    int
	HTTPClient      *http.Client
	Stdout          io.Writer
	UserCodeURL     string
	DeviceTokenURL  string
	TokenURL        string
	VerificationURL string
	OpenBrowser     func(string) error
}

type codexCallbackResult struct {
	Code  string
	State string
	Error string
}

type codexDeviceUserCodeRequest struct {
	ClientID string `json:"client_id"`
}

type codexDeviceUserCodeResponse struct {
	DeviceAuthID string          `json:"device_auth_id"`
	UserCode     string          `json:"user_code"`
	UserCodeAlt  string          `json:"usercode"`
	Interval     json.RawMessage `json:"interval"`
}

type codexDeviceTokenRequest struct {
	DeviceAuthID string `json:"device_auth_id"`
	UserCode     string `json:"user_code"`
}

type codexDeviceTokenResponse struct {
	AuthorizationCode string `json:"authorization_code"`
	CodeVerifier      string `json:"code_verifier"`
	CodeChallenge     string `json:"code_challenge"`
}

func RunCodexAuth(ctx context.Context, opts CodexAuthOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	applyCodexAuthDefaults(&opts)

	if !opts.NoBrowser {
		return runCodexBrowserAuth(ctx, opts)
	}

	userCodeResp, err := requestUserCode(ctx, opts.HTTPClient, opts.UserCodeURL)
	if err != nil {
		return err
	}

	userCode := strings.TrimSpace(userCodeResp.UserCode)
	if userCode == "" {
		userCode = strings.TrimSpace(userCodeResp.UserCodeAlt)
	}
	deviceAuthID := strings.TrimSpace(userCodeResp.DeviceAuthID)
	if userCode == "" || deviceAuthID == "" {
		return fmt.Errorf("codex device auth gerekli alanları döndürmedi")
	}

	fmt.Fprintf(opts.Stdout, "Codex device URL: %s\n", opts.VerificationURL)
	fmt.Fprintf(opts.Stdout, "Codex device code: %s\n", userCode)

	if !opts.NoBrowser && opts.OpenBrowser != nil {
		_ = opts.OpenBrowser(opts.VerificationURL)
	}

	pollInterval := parsePollInterval(userCodeResp.Interval)
	tokenResp, err := pollDeviceToken(ctx, opts.HTTPClient, opts.DeviceTokenURL, deviceAuthID, userCode, pollInterval)
	if err != nil {
		return err
	}

	oauthCfg, err := exchangeAuthorizationCode(ctx, opts.HTTPClient, opts.TokenURL, tokenResp)
	if err != nil {
		return err
	}

	return persistCodexAuth(opts, oauthCfg)
}

func runCodexBrowserAuth(ctx context.Context, opts CodexAuthOptions) error {
	pkce, err := generatePKCECodes()
	if err != nil {
		return err
	}

	state, err := generateState()
	if err != nil {
		return err
	}

	callback, err := startCallbackServer(opts.CallbackPort)
	if err != nil {
		return err
	}
	defer callback.Close(context.Background())

	authURL, err := buildCodexAuthURL(opts.AuthURL, callback.RedirectURL(), state, pkce)
	if err != nil {
		return err
	}

	fmt.Fprintln(opts.Stdout, "Opening browser for Codex authentication")
	if opts.OpenBrowser != nil {
		if err := opts.OpenBrowser(authURL); err != nil {
			return err
		}
	}
	fmt.Fprintln(opts.Stdout, "Waiting for Codex authentication callback...")

	result, err := callback.Wait(5 * time.Minute)
	if err != nil {
		return err
	}
	if result.Error != "" {
		return fmt.Errorf("codex oauth callback hatası: %s", result.Error)
	}
	if result.State != state {
		return fmt.Errorf("codex oauth state uyuşmuyor")
	}

	oauthCfg, err := exchangeAuthorizationCodeWithRedirect(
		ctx,
		opts.HTTPClient,
		opts.TokenURL,
		result.Code,
		pkce.CodeVerifier,
		callback.RedirectURL(),
	)
	if err != nil {
		return err
	}

	return persistCodexAuth(opts, oauthCfg)
}

func persistCodexAuth(opts CodexAuthOptions, oauthCfg *config.OAuthConfig) error {
	cfg, err := config.LoadForAuth(opts.ConfigPath)
	if err != nil {
		return err
	}

	cfg.UpsertProvider(config.Provider{
		Name:     opts.Name,
		Type:     "codex",
		BaseURL:  opts.BaseURL,
		OAuth:    oauthCfg,
		Priority: nextPriority(cfg, opts.Name),
	})

	if err := cfg.Save(opts.ConfigPath); err != nil {
		return err
	}

	fmt.Fprintf(opts.Stdout, "Codex auth kaydedildi: %s\n", opts.Name)
	return nil
}

func applyCodexAuthDefaults(opts *CodexAuthOptions) {
	if opts.ConfigPath == "" {
		opts.ConfigPath = "config.json"
	}
	if opts.Name == "" {
		opts.Name = "codex-oauth"
	}
	if opts.BaseURL == "" {
		opts.BaseURL = defaultCodexBaseURL
	}
	if opts.AuthURL == "" {
		opts.AuthURL = defaultCodexAuthURL
	}
	if opts.CallbackPort == 0 {
		opts.CallbackPort = defaultCodexCallbackPort
	}
	if opts.Stdout == nil {
		opts.Stdout = io.Discard
	}
	if opts.HTTPClient == nil {
		opts.HTTPClient = http.DefaultClient
	}
	if opts.UserCodeURL == "" {
		opts.UserCodeURL = defaultCodexUserCodeURL
	}
	if opts.DeviceTokenURL == "" {
		opts.DeviceTokenURL = defaultCodexDeviceTokenURL
	}
	if opts.TokenURL == "" {
		opts.TokenURL = defaultCodexOAuthTokenURL
	}
	if opts.VerificationURL == "" {
		opts.VerificationURL = defaultCodexVerificationURL
	}
	if opts.OpenBrowser == nil {
		opts.OpenBrowser = openBrowser
	}
}

func requestUserCode(ctx context.Context, client *http.Client, endpoint string) (*codexDeviceUserCodeResponse, error) {
	body, err := json.Marshal(codexDeviceUserCodeRequest{ClientID: codexClientID})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("codex user code isteği başarısız: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed codexDeviceUserCodeResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func pollDeviceToken(ctx context.Context, client *http.Client, endpoint, deviceAuthID, userCode string, interval time.Duration) (*codexDeviceTokenResponse, error) {
	deadline := time.Now().Add(15 * time.Minute)
	for {
		if time.Now().After(deadline) {
			return nil, fmt.Errorf("codex device auth timeout")
		}

		body, err := json.Marshal(codexDeviceTokenRequest{
			DeviceAuthID: deviceAuthID,
			UserCode:     userCode,
		})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}

		respBody, readErr := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if readErr != nil {
			return nil, readErr
		}

		switch {
		case resp.StatusCode >= 200 && resp.StatusCode < 300:
			var parsed codexDeviceTokenResponse
			if err := json.Unmarshal(respBody, &parsed); err != nil {
				return nil, err
			}
			return &parsed, nil
		case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusNotFound:
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(interval):
			}
		default:
			return nil, fmt.Errorf("codex device token polling başarısız: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
		}
	}
}

func exchangeAuthorizationCode(ctx context.Context, client *http.Client, endpoint string, tokenResp *codexDeviceTokenResponse) (*config.OAuthConfig, error) {
	if tokenResp == nil {
		return nil, fmt.Errorf("codex token exchange için device token sonucu gerekli")
	}

	return exchangeAuthorizationCodeWithRedirect(
		ctx,
		client,
		endpoint,
		strings.TrimSpace(tokenResp.AuthorizationCode),
		strings.TrimSpace(tokenResp.CodeVerifier),
		defaultCodexTokenExchangeRedirect,
	)
}

func exchangeAuthorizationCodeWithRedirect(ctx context.Context, client *http.Client, endpoint, code, codeVerifier, redirectURI string) (*config.OAuthConfig, error) {
	form := url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {codexClientID},
		"code":          {strings.TrimSpace(code)},
		"redirect_uri":  {strings.TrimSpace(redirectURI)},
		"code_verifier": {strings.TrimSpace(codeVerifier)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("codex token exchange başarısız: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(parsed.RefreshToken) == "" {
		return nil, fmt.Errorf("codex token exchange refresh_token döndürmedi")
	}
	expiresAt := ""
	if parsed.ExpiresIn > 0 {
		expiresAt = time.Now().Add(time.Duration(parsed.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	return &config.OAuthConfig{
		AccessToken:  strings.TrimSpace(parsed.AccessToken),
		RefreshToken: strings.TrimSpace(parsed.RefreshToken),
		IDToken:      strings.TrimSpace(parsed.IDToken),
		ExpiresAt:    expiresAt,
	}, nil
}

func parsePollInterval(raw json.RawMessage) time.Duration {
	defaultInterval := 5 * time.Second
	if len(raw) == 0 {
		return defaultInterval
	}

	var asInt int
	if err := json.Unmarshal(raw, &asInt); err == nil && asInt > 0 {
		return time.Duration(asInt) * time.Second
	}

	var asString string
	if err := json.Unmarshal(raw, &asString); err == nil {
		if seconds, convErr := time.ParseDuration(strings.TrimSpace(asString) + "s"); convErr == nil && seconds > 0 {
			return seconds
		}
	}

	return defaultInterval
}

func nextPriority(cfg *config.Config, name string) int {
	maxPriority := 0
	for _, provider := range cfg.Providers {
		if provider.Name == name && provider.Priority > 0 {
			return provider.Priority
		}
		if provider.Priority > maxPriority {
			maxPriority = provider.Priority
		}
	}
	return maxPriority + 1
}

func openBrowser(rawURL string) error {
	var command string
	switch runtime.GOOS {
	case "darwin":
		command = "open"
	case "linux":
		command = "xdg-open"
	default:
		return nil
	}
	return exec.Command(command, rawURL).Start()
}

type pkceCodes struct {
	CodeVerifier  string
	CodeChallenge string
}

func generatePKCECodes() (*pkceCodes, error) {
	random := make([]byte, 96)
	if _, err := rand.Read(random); err != nil {
		return nil, err
	}
	verifier := base64.RawURLEncoding.EncodeToString(random)
	sum := sha256.Sum256([]byte(verifier))
	return &pkceCodes{
		CodeVerifier:  verifier,
		CodeChallenge: base64.RawURLEncoding.EncodeToString(sum[:]),
	}, nil
}

func generateState() (string, error) {
	random := make([]byte, 24)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func buildCodexAuthURL(baseURL, redirectURI, state string, pkce *pkceCodes) (string, error) {
	u, err := url.Parse(baseURL)
	if err != nil {
		return "", err
	}
	q := u.Query()
	q.Set("client_id", codexClientID)
	q.Set("response_type", "code")
	q.Set("redirect_uri", redirectURI)
	q.Set("scope", "openid email profile offline_access")
	q.Set("state", state)
	q.Set("code_challenge", pkce.CodeChallenge)
	q.Set("code_challenge_method", "S256")
	q.Set("prompt", "login")
	q.Set("id_token_add_organizations", "true")
	q.Set("codex_cli_simplified_flow", "true")
	u.RawQuery = q.Encode()
	return u.String(), nil
}

type codexCallbackServer struct {
	listener net.Listener
	server   *http.Server
	resultCh chan codexCallbackResult
}

func startCallbackServer(port int) (*codexCallbackServer, error) {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return nil, err
	}

	s := &codexCallbackServer{
		listener: ln,
		resultCh: make(chan codexCallbackResult, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/auth/callback", s.handleCallback)
	mux.HandleFunc("/success", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, "<html><body>Codex authentication completed. You can close this tab.</body></html>")
	})

	s.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		_ = s.server.Serve(ln)
	}()

	return s, nil
}

func (s *codexCallbackServer) RedirectURL() string {
	_, port, err := net.SplitHostPort(s.listener.Addr().String())
	if err != nil {
		return fmt.Sprintf("http://localhost:%d/auth/callback", defaultCodexCallbackPort)
	}
	return "http://localhost:" + port + "/auth/callback"
}

func (s *codexCallbackServer) Wait(timeout time.Duration) (*codexCallbackResult, error) {
	select {
	case result := <-s.resultCh:
		return &result, nil
	case <-time.After(timeout):
		return nil, fmt.Errorf("timeout waiting for OAuth callback")
	}
}

func (s *codexCallbackServer) Close(ctx context.Context) error {
	if s.server == nil {
		return nil
	}
	return s.server.Shutdown(ctx)
}

func (s *codexCallbackServer) handleCallback(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	result := codexCallbackResult{
		Code:  query.Get("code"),
		State: query.Get("state"),
		Error: query.Get("error"),
	}
	select {
	case s.resultCh <- result:
	default:
	}
	http.Redirect(w, r, "/success", http.StatusFound)
}
