package provider

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/convert"
	"opencode-proxy/internal/middleware"
	"opencode-proxy/internal/openai"
	"opencode-proxy/internal/sse"
)

type OpenAIProvider struct {
	name     string
	priority int
	baseURL  string
	apiKey   string
	client   *http.Client
	logger   *slog.Logger
}

func NewOpenAI(cfg config.Provider, client *http.Client, logger *slog.Logger) *OpenAIProvider {
	return &OpenAIProvider{
		name:     cfg.Name,
		priority: cfg.Priority,
		baseURL:  cfg.BaseURL,
		apiKey:   cfg.APIKey,
		client:   client,
		logger:   logger,
	}
}

func (p *OpenAIProvider) Name() string  { return p.name }
func (p *OpenAIProvider) Priority() int { return p.priority }

func (p *OpenAIProvider) Proxy(ctx context.Context, w http.ResponseWriter, _ []byte, antReq anthropic.Request) error {
	reqID := middleware.GetRequestID(ctx)

	oaiReq := convert.ToOpenAI(antReq)
	oaiBody, err := json.Marshal(oaiReq)
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: false}
	}

	proxyReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL, strings.NewReader(string(oaiBody)))
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Authorization", "Bearer "+p.apiKey)

	p.logger.Debug("openai istek gönderiliyor",
		slog.String("provider", p.name),
		slog.String("request_id", reqID),
	)

	resp, err := p.client.Do(proxyReq)
	if err != nil {
		return &ProxyError{ProviderName: p.name, Message: err.Error(), Retryable: true}
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		p.logger.Warn("openai sağlayıcı başarısız",
			slog.String("provider", p.name),
			slog.Int("status", resp.StatusCode),
			slog.String("body", string(respBody)),
			slog.String("request_id", reqID),
		)
		return &ProxyError{
			ProviderName: p.name,
			StatusCode:   resp.StatusCode,
			Message:      string(respBody),
			Retryable:    isRetryable(resp.StatusCode),
		}
	}

	p.logger.Info("openai proxy başarılı",
		slog.String("provider", p.name),
		slog.String("request_id", reqID),
	)

	if antReq.Stream {
		p.streamResponse(w, resp, antReq.Model)
	} else {
		p.nonStreamResponse(w, resp, antReq.Model)
	}
	return nil
}

func (p *OpenAIProvider) nonStreamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		sse.WriteError(w, http.StatusInternalServerError, "yanıt okunamadı: "+err.Error())
		return
	}

	var oaiResp openai.Response
	if err := json.Unmarshal(body, &oaiResp); err != nil {
		sse.WriteError(w, http.StatusInternalServerError, "yanıt ayrıştırılamadı: "+err.Error())
		return
	}

	var content []any
	stopReason := "end_turn"

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]

		// Thinking block'u content'in başına ekle
		if choice.Message.ReasoningContent != nil && *choice.Message.ReasoningContent != "" {
			content = append(content, anthropic.ThinkingBlock{
				Type:     "thinking",
				Thinking: *choice.Message.ReasoningContent,
			})
		}

		if choice.Message.Content != nil && *choice.Message.Content != "" {
			content = append(content, anthropic.TextBlock{Type: "text", Text: *choice.Message.Content})
		}
		for _, tc := range choice.Message.ToolCalls {
			input := tc.Function.Arguments
			if input == "" {
				input = "{}"
			}
			content = append(content, anthropic.ToolUseBlock{
				Type:  "tool_use",
				ID:    tc.ID,
				Name:  tc.Function.Name,
				Input: json.RawMessage(input),
			})
			stopReason = "tool_use"
		}
		if choice.FinishReason != nil && *choice.FinishReason == "length" {
			stopReason = "max_tokens"
		}
	}

	if len(content) == 0 {
		content = append(content, anthropic.TextBlock{Type: "text", Text: ""})
	}

	respBody, _ := json.Marshal(map[string]any{
		"id":            "msg-" + oaiResp.ID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]any{
			"input_tokens":  oaiResp.Usage.PromptTokens,
			"output_tokens": oaiResp.Usage.CompletionTokens,
		},
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("anthropic-version", "2023-06-01")
	w.Write(respBody)
}

func (p *OpenAIProvider) streamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		sse.WriteError(w, http.StatusInternalServerError, "streaming desteklenmiyor")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("anthropic-version", "2023-06-01")

	msgID := "msg-proxy-" + genMessageID()

	sse.Send(w, flusher, "message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []any{}, "model": model,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 0, "output_tokens": 0},
		},
	})

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	type blockState struct {
		idx   int
		open  bool
		bType string // "thinking" veya "text"
	}

	thinkingBlock := blockState{idx: 0, bType: "thinking"}
	textBlock := blockState{idx: 1, bType: "text"}
	nextBlockIdx := 2
	thinkingOpen := false
	textOpen := false

	outputTokens := 0
	pending := make(map[int]*openai.ToolCallAccumulator)

	closeBlock := func(bs *blockState) {
		if !bs.open {
			return
		}
		sse.Send(w, flusher, "content_block_stop", map[string]any{
			"type": "content_block_stop", "index": bs.idx,
		})
		bs.open = false
	}

	// İlk reasoning_content geldiğinde thinking block aç
	ensureThinkingOpen := func() {
		if thinkingOpen {
			return
		}
		sse.Send(w, flusher, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         thinkingBlock.idx,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		})
		thinkingBlock.open = true
		thinkingOpen = true
	}

	// İlk text geldiğinde text block aç
	ensureTextOpen := func() {
		if textOpen {
			return
		}
		closeBlock(&thinkingBlock)
		thinkingOpen = false
		sse.Send(w, flusher, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         textBlock.idx,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		textBlock.open = true
		textOpen = true
	}

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		data := strings.TrimPrefix(line, "data: ")
		if data == "[DONE]" {
			break
		}

		var chunk openai.StreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		choice := chunk.Choices[0]

		for _, tcDelta := range choice.Delta.ToolCalls {
			acc, exists := pending[tcDelta.Index]
			if !exists {
				acc = &openai.ToolCallAccumulator{ID: tcDelta.ID, Name: tcDelta.Function.Name}
				pending[tcDelta.Index] = acc
			}
			acc.Arguments += tcDelta.Function.Arguments
		}

		// Reasoning content → thinking block
		if choice.Delta.ReasoningContent != "" {
			ensureThinkingOpen()
			outputTokens++
			sse.Send(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": thinkingBlock.idx,
				"delta": map[string]any{"type": "thinking_delta", "thinking": choice.Delta.ReasoningContent},
			})
		}

		// Normal content → text block
		if choice.Delta.Content != "" {
			ensureTextOpen()
			outputTokens++
			sse.Send(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": textBlock.idx,
				"delta": map[string]any{"type": "text_delta", "text": choice.Delta.Content},
			})
		}

		if choice.FinishReason != nil {
			closeBlock(&thinkingBlock)
			thinkingOpen = false
			closeBlock(&textBlock)
			textOpen = false

			// Tool call block'ları - Anthropic streaming protokolüne uygun:
			// content_block_start: input = {}
			// content_block_delta: input_json_delta ile partial_json
			// content_block_stop
			// Index'leri sıralı şekilde işle (map key sıralı olmayabilir)
			sortedIndices := make([]int, 0, len(pending))
			for idx := range pending {
				sortedIndices = append(sortedIndices, idx)
			}
			slices.Sort(sortedIndices)
			for _, i := range sortedIndices {
				tc := pending[i]
				args := tc.Arguments
				if args == "" {
					args = "{}"
				}
				idx := nextBlockIdx
				nextBlockIdx++
				sse.Send(w, flusher, "content_block_start", map[string]any{
					"type":  "content_block_start",
					"index": idx,
					"content_block": map[string]any{
						"type": "tool_use", "id": tc.ID, "name": tc.Name,
						"input": map[string]any{},
					},
				})
				sse.Send(w, flusher, "content_block_delta", map[string]any{
					"type":  "content_block_delta",
					"index": idx,
					"delta": map[string]any{
						"type":         "input_json_delta",
						"partial_json": args,
					},
				})
				sse.Send(w, flusher, "content_block_stop", map[string]any{
					"type": "content_block_stop", "index": idx,
				})
			}

			stopReason := "end_turn"
			if len(pending) > 0 {
				stopReason = "tool_use"
			} else if *choice.FinishReason == "length" {
				stopReason = "max_tokens"
			}

			sse.Send(w, flusher, "message_delta", map[string]any{
				"type":  "message_delta",
				"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
				"usage": map[string]any{"output_tokens": outputTokens},
			})
			sse.Send(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
		}
	}
}

func genMessageID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
