package provider

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/sse"
)

const (
	codexClientID  = "app_EMoamEEZ73f0CkXaXp7hrann"
	codexUserAgent = "codex-tui/0.118.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9 (codex-tui; 0.118.0)"
)

var codexTokenURL = "https://auth.openai.com/oauth/token"

type CodexProvider struct {
	name         string
	priority     int
	baseURL      string
	apiKey       string
	client       *http.Client
	logger       *slog.Logger
	persistOAuth func(config.OAuthConfig) error

	mu    sync.Mutex
	oauth codexOAuthState
}

type codexOAuthState struct {
	AccessToken  string
	RefreshToken string
	IDToken      string
	AccountID    string
	Email        string
	ExpiresAt    string
}

type codexRequest struct {
	Model             string         `json:"model"`
	Instructions      string         `json:"instructions"`
	Input             []codexInput   `json:"input"`
	Tools             []codexTool    `json:"tools,omitempty"`
	ToolChoice        string         `json:"tool_choice,omitempty"`
	ParallelToolCalls bool           `json:"parallel_tool_calls,omitempty"`
	Reasoning         codexReasoning `json:"reasoning"`
	Stream            bool           `json:"stream"`
	Store             bool           `json:"store"`
	Include           []string       `json:"include,omitempty"`
}

type codexReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
}

type codexInput struct {
	Type    string             `json:"type"`
	Role    string             `json:"role,omitempty"`
	Content []codexContentPart `json:"content,omitempty"`
	CallID  string             `json:"call_id,omitempty"`
	Name    string             `json:"name,omitempty"`
	Output  any                `json:"output,omitempty"`
	Args    string             `json:"arguments,omitempty"`
}

type codexContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

type codexTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name,omitempty"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
	Strict      bool            `json:"strict"`
}

type codexResponseEnvelope struct {
	Type     string                `json:"type"`
	Response codexCompletedPayload `json:"response"`
	Item     codexOutputItem       `json:"item"`
	Delta    string                `json:"delta"`
	Args     string                `json:"arguments"`
}

type codexCompletedPayload struct {
	ID         string            `json:"id"`
	Model      string            `json:"model"`
	StopReason string            `json:"stop_reason"`
	Output     []codexOutputItem `json:"output"`
	Usage      codexUsage        `json:"usage"`
}

type codexUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

type codexOutputItem struct {
	Type             string             `json:"type"`
	CallID           string             `json:"call_id"`
	Name             string             `json:"name"`
	Arguments        string             `json:"arguments"`
	EncryptedContent string             `json:"encrypted_content"`
	Content          []codexContentPart `json:"content"`
	Summary          []codexContentPart `json:"summary"`
}

type codexStreamState struct {
	msgID            string
	textBlockOpen    bool
	textBlockIndex   int
	nextContentIndex int
	toolCallOpen     bool
	toolCallIndex    int
	hasToolCall      bool
	hasReceivedText  bool
	hasReceivedArgs  bool
	outputTokens     int
	shortToOriginal  map[string]string
}

func NewCodex(cfg config.Provider, client *http.Client, logger *slog.Logger) *CodexProvider {
	state := codexOAuthState{}
	if cfg.OAuth != nil {
		state = codexOAuthState{
			AccessToken:  cfg.OAuth.AccessToken,
			RefreshToken: cfg.OAuth.RefreshToken,
			IDToken:      cfg.OAuth.IDToken,
			AccountID:    cfg.OAuth.AccountID,
			Email:        cfg.OAuth.Email,
			ExpiresAt:    cfg.OAuth.ExpiresAt,
		}
	}

	return &CodexProvider{
		name:         cfg.Name,
		priority:     cfg.Priority,
		baseURL:      cfg.BaseURL,
		apiKey:       cfg.APIKey,
		client:       client,
		logger:       logger,
		persistOAuth: cfg.PersistOAuth,
		oauth:        state,
	}
}

func (p *CodexProvider) Name() string  { return p.name }
func (p *CodexProvider) Priority() int { return p.priority }

func (p *CodexProvider) Proxy(ctx context.Context, w http.ResponseWriter, _ []byte, antReq anthropic.Request) error {
	token, err := p.accessToken(ctx)
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}

	reqBody, shortToOriginal, err := buildCodexRequest(antReq)
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: false}
	}

	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, codexResponsesURL(p.baseURL), bytes.NewReader(reqBody))
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}

	applyCodexRequestHeaders(proxyReq, token, antReq.Stream)

	resp, err := p.client.Do(proxyReq)
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized && p.canRefresh() {
		token, err = p.forceRefresh(ctx)
		if err != nil {
			return &ProxyError{ProviderName: p.name, StatusCode: http.StatusUnauthorized, Message: err.Error(), Retryable: false}
		}

		retryReq, retryErr := http.NewRequestWithContext(ctx, http.MethodPost, codexResponsesURL(p.baseURL), bytes.NewReader(reqBody))
		if retryErr != nil {
			return &ProxyError{ProviderName: p.name, Message: retryErr.Error(), Retryable: true}
		}
		applyCodexRequestHeaders(retryReq, token, antReq.Stream)
		retryResp, retryErr := p.client.Do(retryReq)
		if retryErr != nil {
			return &ProxyError{ProviderName: p.name, Message: retryErr.Error(), Retryable: true}
		}
		defer retryResp.Body.Close()
		resp = retryResp
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		respBody, _ := io.ReadAll(resp.Body)
		return &ProxyError{
			ProviderName: p.name,
			StatusCode:   resp.StatusCode,
			Message:      string(respBody),
			Retryable:    isRetryable(resp.StatusCode),
		}
	}

	if antReq.Stream {
		return p.streamResponse(w, resp, antReq.Model, shortToOriginal)
	}
	return p.nonStreamResponse(w, resp, antReq.Model, shortToOriginal)
}

func (p *CodexProvider) nonStreamResponse(w http.ResponseWriter, resp *http.Response, model string, shortToOriginal map[string]string) error {
	envelope, err := readCompletedEnvelope(resp.Body)
	if err != nil {
		sse.WriteError(w, http.StatusInternalServerError, "yanıt ayrıştırılamadı: "+err.Error())
		return nil
	}

	out := map[string]any{
		"id":            envelope.Response.ID,
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"content":       []any{},
		"stop_reason":   codexStopReason(envelope.Response.StopReason, hasFunctionCall(envelope.Response.Output)),
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  envelope.Response.Usage.InputTokens,
			"output_tokens": envelope.Response.Usage.OutputTokens,
		},
	}

	content := make([]any, 0, len(envelope.Response.Output))
	for _, item := range envelope.Response.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					content = append(content, map[string]any{"type": "text", "text": part.Text})
				}
			}
		case "function_call":
			name := restoreToolName(item.Name, shortToOriginal)
			input := json.RawMessage("{}")
			if strings.TrimSpace(item.Arguments) != "" && json.Valid([]byte(item.Arguments)) {
				input = json.RawMessage(item.Arguments)
			}
			content = append(content, map[string]any{
				"type":  "tool_use",
				"id":    sanitizeClaudeToolID(item.CallID),
				"name":  name,
				"input": input,
			})
		case "reasoning":
			thinking := buildThinkingText(item)
			if thinking != "" || item.EncryptedContent != "" {
				block := map[string]any{"type": "thinking", "thinking": thinking}
				if item.EncryptedContent != "" {
					block["signature"] = item.EncryptedContent
				}
				content = append(content, block)
			}
		}
	}
	out["content"] = content

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("anthropic-version", "2023-06-01")
	return json.NewEncoder(w).Encode(out)
}

func readCompletedEnvelope(reader io.Reader) (*codexResponseEnvelope, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var envelope codexResponseEnvelope
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}
		if envelope.Type == "response.completed" {
			return &envelope, nil
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("codex tamamlanmış yanıt dönmedi")
}

func (p *CodexProvider) streamResponse(w http.ResponseWriter, resp *http.Response, model string, shortToOriginal map[string]string) error {
	flusher, ok := w.(http.Flusher)
	if !ok {
		sse.WriteError(w, http.StatusInternalServerError, "streaming desteklenmiyor")
		return nil
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("anthropic-version", "2023-06-01")

	state := &codexStreamState{
		msgID:            "msg-codex-" + genMessageID(),
		textBlockIndex:   0,
		nextContentIndex: 1,
		shortToOriginal:  shortToOriginal,
	}

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			break
		}

		var envelope codexResponseEnvelope
		if err := json.Unmarshal([]byte(payload), &envelope); err != nil {
			continue
		}

		switch envelope.Type {
		case "response.created":
			sse.Send(w, flusher, "message_start", map[string]any{
				"type": "message_start",
				"message": map[string]any{
					"id": state.msgID, "type": "message", "role": "assistant",
					"content": []any{}, "model": model,
					"stop_reason": nil, "stop_sequence": nil,
					"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
				},
			})
		case "response.content_part.added":
			if !state.textBlockOpen {
				sse.Send(w, flusher, "content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         state.textBlockIndex,
					"content_block": map[string]any{"type": "text", "text": ""},
				})
				state.textBlockOpen = true
			}
		case "response.output_text.delta":
			if !state.textBlockOpen {
				sse.Send(w, flusher, "content_block_start", map[string]any{
					"type":          "content_block_start",
					"index":         state.textBlockIndex,
					"content_block": map[string]any{"type": "text", "text": ""},
				})
				state.textBlockOpen = true
			}
			state.outputTokens++
			state.hasReceivedText = true
			sse.Send(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": state.textBlockIndex,
				"delta": map[string]any{"type": "text_delta", "text": envelope.Delta},
			})
		case "response.content_part.done":
			if state.textBlockOpen {
				sse.Send(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": state.textBlockIndex,
				})
				state.textBlockOpen = false
			}
		case "response.output_item.added":
			if envelope.Item.Type == "function_call" {
				if state.textBlockOpen {
					sse.Send(w, flusher, "content_block_stop", map[string]any{
						"type": "content_block_stop", "index": state.textBlockIndex,
					})
					state.textBlockOpen = false
				}
				state.hasToolCall = true
				state.toolCallOpen = true
				state.hasReceivedArgs = false
				state.toolCallIndex = state.nextContentIndex
				state.nextContentIndex++
				sse.Send(w, flusher, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": state.toolCallIndex,
					"content_block": map[string]any{
						"type":  "tool_use",
						"id":    sanitizeClaudeToolID(envelope.Item.CallID),
						"name":  restoreToolName(envelope.Item.Name, shortToOriginal),
						"input": map[string]any{},
					},
				})
			}
		case "response.function_call_arguments.delta":
			if state.toolCallOpen {
				state.hasReceivedArgs = true
				sse.Send(w, flusher, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": state.toolCallIndex,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": envelope.Delta},
				})
			}
		case "response.function_call_arguments.done":
			if state.toolCallOpen && !state.hasReceivedArgs && envelope.Args != "" {
				sse.Send(w, flusher, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": state.toolCallIndex,
					"delta": map[string]any{"type": "input_json_delta", "partial_json": envelope.Args},
				})
			}
		case "response.output_item.done":
			switch envelope.Item.Type {
			case "function_call":
				if state.toolCallOpen {
					sse.Send(w, flusher, "content_block_stop", map[string]any{
						"type": "content_block_stop", "index": state.toolCallIndex,
					})
					state.toolCallOpen = false
					state.hasReceivedArgs = false
				}
			case "message":
				if state.hasReceivedText {
					break
				}
				text := extractCodexMessageText(envelope.Item)
				if text == "" {
					break
				}
				if !state.textBlockOpen {
					sse.Send(w, flusher, "content_block_start", map[string]any{
						"type":          "content_block_start",
						"index":         state.textBlockIndex,
						"content_block": map[string]any{"type": "text", "text": ""},
					})
					state.textBlockOpen = true
				}
				state.hasReceivedText = true
				sse.Send(w, flusher, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": state.textBlockIndex,
					"delta": map[string]any{"type": "text_delta", "text": text},
				})
				sse.Send(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": state.textBlockIndex,
				})
				state.textBlockOpen = false
			}
		case "response.completed":
			if state.textBlockOpen {
				sse.Send(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": state.textBlockIndex,
				})
				state.textBlockOpen = false
			}
			if state.toolCallOpen {
				sse.Send(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": state.toolCallIndex,
				})
				state.toolCallOpen = false
			}

			outputTokens := envelope.Response.Usage.OutputTokens
			if outputTokens == 0 {
				outputTokens = state.outputTokens
			}
			sse.Send(w, flusher, "message_delta", map[string]any{
				"type": "message_delta",
				"delta": map[string]any{
					"stop_reason":   codexStopReason(envelope.Response.StopReason, state.hasToolCall),
					"stop_sequence": nil,
				},
				"usage": map[string]any{
					"input_tokens":  envelope.Response.Usage.InputTokens,
					"output_tokens": outputTokens,
				},
			})
			sse.Send(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
		}
	}

	if err := scanner.Err(); err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	return nil
}

func (p *CodexProvider) accessToken(ctx context.Context) (string, error) {
	if strings.TrimSpace(p.apiKey) != "" {
		return strings.TrimSpace(p.apiKey), nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	if token := strings.TrimSpace(p.oauth.AccessToken); token != "" && !codexTokenExpired(p.oauth.ExpiresAt) {
		return token, nil
	}

	return p.refreshLocked(ctx)
}

func (p *CodexProvider) forceRefresh(ctx context.Context) (string, error) {
	if strings.TrimSpace(p.apiKey) != "" {
		return strings.TrimSpace(p.apiKey), nil
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	return p.refreshLocked(ctx)
}

func (p *CodexProvider) canRefresh() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.TrimSpace(p.oauth.RefreshToken) != ""
}

func (p *CodexProvider) refreshLocked(ctx context.Context) (string, error) {
	refreshToken := strings.TrimSpace(p.oauth.RefreshToken)
	if refreshToken == "" {
		if token := strings.TrimSpace(p.oauth.AccessToken); token != "" {
			return token, nil
		}
		return "", fmt.Errorf("codex oauth refresh token yok")
	}

	form := url.Values{
		"client_id":     {codexClientID},
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"scope":         {"openid profile email"},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, codexTokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("codex token refresh başarısız: HTTP %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp struct {
		AccessToken  string `json:"access_token"`
		RefreshToken string `json:"refresh_token"`
		IDToken      string `json:"id_token"`
		ExpiresIn    int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", err
	}
	if strings.TrimSpace(tokenResp.AccessToken) == "" {
		return "", fmt.Errorf("codex token refresh access_token döndürmedi")
	}

	p.oauth.AccessToken = strings.TrimSpace(tokenResp.AccessToken)
	if strings.TrimSpace(tokenResp.RefreshToken) != "" {
		p.oauth.RefreshToken = strings.TrimSpace(tokenResp.RefreshToken)
	}
	if strings.TrimSpace(tokenResp.IDToken) != "" {
		p.oauth.IDToken = strings.TrimSpace(tokenResp.IDToken)
	}
	if tokenResp.ExpiresIn > 0 {
		p.oauth.ExpiresAt = time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)
	}
	if p.persistOAuth != nil {
		if err := p.persistOAuth(config.OAuthConfig{
			AccessToken:  p.oauth.AccessToken,
			RefreshToken: p.oauth.RefreshToken,
			IDToken:      p.oauth.IDToken,
			AccountID:    p.oauth.AccountID,
			Email:        p.oauth.Email,
			ExpiresAt:    p.oauth.ExpiresAt,
		}); err != nil {
			return "", err
		}
	}

	return p.oauth.AccessToken, nil
}

func buildCodexRequest(req anthropic.Request) ([]byte, map[string]string, error) {
	shortMap := buildShortNameMap(req.Tools)
	shortToOriginal := make(map[string]string, len(shortMap))
	for original, short := range shortMap {
		shortToOriginal[short] = original
	}

	out := codexRequest{
		Model:             req.Model,
		Instructions:      "",
		Input:             make([]codexInput, 0, len(req.Messages)+1),
		ParallelToolCalls: true,
		Reasoning: codexReasoning{
			Effort:  "medium",
			Summary: "auto",
		},
		Stream:  true,
		Store:   false,
		Include: []string{"reasoning.encrypted_content"},
	}

	systemText := joinSystemText(req.System)
	if systemText != "" {
		out.Instructions = systemText
		out.Input = append(out.Input, codexInput{
			Type: "message",
			Role: "developer",
			Content: []codexContentPart{{
				Type: "input_text",
				Text: systemText,
			}},
		})
	}

	for _, msg := range req.Messages {
		inputs, err := convertAnthropicMessageToCodex(msg, shortMap)
		if err != nil {
			return nil, nil, err
		}
		out.Input = append(out.Input, inputs...)
	}

	if len(req.Tools) > 0 {
		out.Tools = make([]codexTool, 0, len(req.Tools))
		out.ToolChoice = "auto"
		for _, tool := range req.Tools {
			params := normalizeToolParameters(tool.InputSchema)
			out.Tools = append(out.Tools, codexTool{
				Type:        "function",
				Name:        shortMap[tool.Name],
				Description: tool.Description,
				Parameters:  params,
				Strict:      false,
			})
		}
	}

	body, err := json.Marshal(out)
	if err != nil {
		return nil, nil, err
	}
	return body, shortToOriginal, nil
}

func convertAnthropicMessageToCodex(msg anthropic.Message, shortMap map[string]string) ([]codexInput, error) {
	var plain string
	if err := json.Unmarshal(msg.Content, &plain); err == nil {
		partType := "input_text"
		if msg.Role == "assistant" {
			partType = "output_text"
		}
		return []codexInput{{
			Type: "message",
			Role: msg.Role,
			Content: []codexContentPart{{
				Type: partType,
				Text: plain,
			}},
		}}, nil
	}

	var blocks []json.RawMessage
	if err := json.Unmarshal(msg.Content, &blocks); err != nil {
		return nil, fmt.Errorf("anthropic message parse hatası: %w", err)
	}

	results := make([]codexInput, 0, len(blocks))
	message := codexInput{
		Type:    "message",
		Role:    msg.Role,
		Content: []codexContentPart{},
	}
	partType := "input_text"
	if msg.Role == "assistant" {
		partType = "output_text"
	}

	flushMessage := func() {
		if len(message.Content) == 0 {
			return
		}
		results = append(results, message)
		message = codexInput{
			Type:    "message",
			Role:    msg.Role,
			Content: []codexContentPart{},
		}
	}

	for _, raw := range blocks {
		var header struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &header); err != nil {
			continue
		}

		switch header.Type {
		case "text":
			var block anthropic.TextBlock
			if err := json.Unmarshal(raw, &block); err == nil && block.Text != "" {
				message.Content = append(message.Content, codexContentPart{
					Type: partType,
					Text: block.Text,
				})
			}
		case "tool_use":
			flushMessage()
			var block anthropic.ToolUseBlock
			if err := json.Unmarshal(raw, &block); err == nil {
				name := block.Name
				if short, ok := shortMap[name]; ok {
					name = short
				}
				args := strings.TrimSpace(string(block.Input))
				if args == "" || args == "null" {
					args = "{}"
				}
				results = append(results, codexInput{
					Type:   "function_call",
					CallID: block.ID,
					Name:   name,
					Args:   args,
				})
			}
		case "tool_result":
			flushMessage()
			var block anthropic.ToolResultBlock
			if err := json.Unmarshal(raw, &block); err == nil {
				results = append(results, codexInput{
					Type:   "function_call_output",
					CallID: block.ToolUseID,
					Output: block.ContentText(),
				})
			}
		case "image":
			var block struct {
				Source struct {
					Data      string `json:"data"`
					Base64    string `json:"base64"`
					MediaType string `json:"media_type"`
					MimeType  string `json:"mime_type"`
				} `json:"source"`
			}
			if err := json.Unmarshal(raw, &block); err == nil {
				data := strings.TrimSpace(block.Source.Data)
				if data == "" {
					data = strings.TrimSpace(block.Source.Base64)
				}
				if data != "" {
					mediaType := strings.TrimSpace(block.Source.MediaType)
					if mediaType == "" {
						mediaType = strings.TrimSpace(block.Source.MimeType)
					}
					if mediaType == "" {
						mediaType = "application/octet-stream"
					}
					message.Content = append(message.Content, codexContentPart{
						Type:     "input_image",
						ImageURL: fmt.Sprintf("data:%s;base64,%s", mediaType, data),
					})
				}
			}
		}
	}

	flushMessage()
	return results, nil
}

func joinSystemText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}

	var plain string
	if err := json.Unmarshal(raw, &plain); err == nil {
		return strings.TrimSpace(plain)
	}

	var blocks []anthropic.TextBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		parts := make([]string, 0, len(blocks))
		for _, block := range blocks {
			text := strings.TrimSpace(block.Text)
			if text != "" && !strings.HasPrefix(text, "x-anthropic-billing-header: ") {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	}

	return ""
}

func buildShortNameMap(tools []anthropic.Tool) map[string]string {
	const limit = 64
	used := make(map[string]struct{}, len(tools))
	out := make(map[string]string, len(tools))

	baseCandidate := func(name string) string {
		if len(name) <= limit {
			return name
		}
		if strings.HasPrefix(name, "mcp__") {
			idx := strings.LastIndex(name, "__")
			if idx > 0 {
				candidate := "mcp__" + name[idx+2:]
				if len(candidate) > limit {
					return candidate[:limit]
				}
				return candidate
			}
		}
		return name[:limit]
	}

	makeUnique := func(candidate string) string {
		if _, ok := used[candidate]; !ok {
			return candidate
		}
		base := candidate
		for i := 1; ; i++ {
			suffix := fmt.Sprintf("_%d", i)
			maxLen := limit - len(suffix)
			if maxLen < 0 {
				maxLen = 0
			}
			next := base
			if len(next) > maxLen {
				next = next[:maxLen]
			}
			next += suffix
			if _, ok := used[next]; !ok {
				return next
			}
		}
	}

	for _, tool := range tools {
		candidate := baseCandidate(tool.Name)
		unique := makeUnique(candidate)
		used[unique] = struct{}{}
		out[tool.Name] = unique
	}

	return out
}

func normalizeToolParameters(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 || !json.Valid(raw) {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}

	var obj map[string]any
	if err := json.Unmarshal(raw, &obj); err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	if obj["type"] == nil {
		obj["type"] = "object"
	}
	if obj["type"] == "object" && obj["properties"] == nil {
		obj["properties"] = map[string]any{}
	}
	delete(obj, "$schema")

	normalized, err := json.Marshal(obj)
	if err != nil {
		return json.RawMessage(`{"type":"object","properties":{}}`)
	}
	return normalized
}

func applyCodexRequestHeaders(req *http.Request, token string, stream bool) {
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("User-Agent", codexUserAgent)
	req.Header.Set("Originator", "codex-tui")
	req.Header.Set("Connection", "Keep-Alive")
	req.Header.Set("Session_id", genMessageID())
	if stream {
		req.Header.Set("Accept", "text/event-stream")
	} else {
		req.Header.Set("Accept", "application/json")
	}
}

func codexResponsesURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if strings.HasSuffix(trimmed, "/responses") {
		return trimmed
	}
	return trimmed + "/responses"
}

func codexStopReason(upstream string, hasToolCall bool) string {
	if hasToolCall {
		return "tool_use"
	}
	switch upstream {
	case "max_tokens", "length":
		return "max_tokens"
	case "stop":
		return "end_turn"
	case "tool_use":
		return "tool_use"
	default:
		return "end_turn"
	}
}

func hasFunctionCall(items []codexOutputItem) bool {
	for _, item := range items {
		if item.Type == "function_call" {
			return true
		}
	}
	return false
}

func restoreToolName(name string, shortToOriginal map[string]string) string {
	if original, ok := shortToOriginal[name]; ok {
		return original
	}
	return name
}

func buildThinkingText(item codexOutputItem) string {
	parts := make([]string, 0, len(item.Summary)+len(item.Content))
	for _, part := range item.Summary {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	for _, part := range item.Content {
		if strings.TrimSpace(part.Text) != "" {
			parts = append(parts, strings.TrimSpace(part.Text))
		}
	}
	return strings.Join(parts, "")
}

func extractCodexMessageText(item codexOutputItem) string {
	parts := make([]string, 0, len(item.Content))
	for _, part := range item.Content {
		if part.Type == "output_text" && strings.TrimSpace(part.Text) != "" {
			parts = append(parts, part.Text)
		}
	}
	return strings.Join(parts, "")
}

func codexTokenExpired(expiresAt string) bool {
	expiresAt = strings.TrimSpace(expiresAt)
	if expiresAt == "" {
		return true
	}
	t, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return true
	}
	return time.Now().Add(30 * time.Second).After(t)
}

func sanitizeClaudeToolID(id string) string {
	var b strings.Builder
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r)
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_' || r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	if b.Len() == 0 {
		return "toolu_" + genMessageID()
	}
	return b.String()
}
