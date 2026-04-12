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

	p.logger.Info("openai request params",
		slog.String("provider", p.name),
		slog.String("model", oaiReq.Model),
		slog.Int("max_tokens", oaiReq.MaxTokens),
		slog.Bool("stream", oaiReq.Stream),
		slog.Bool("thinking_enabled", oaiReq.ChatTemplateKwargs != nil && oaiReq.ChatTemplateKwargs.EnableThinking),
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
		reasoning := firstNonEmptyPtr(
			choice.Message.ReasoningContent,
			choice.Message.Reasoning,
			choice.Message.Thinking,
		)
		if reasoning != "" {
			content = append(content, anthropic.ThinkingBlock{
				Type:     "thinking",
				Thinking: reasoning,
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

	type blockState struct {
		idx  int
		open bool
	}

	thinkingBlock := blockState{idx: 0}
	textBlock := blockState{idx: 1}
	nextBlockIdx := 2
	outputTokens := 0
	pending := make(map[int]*openai.ToolCallAccumulator)
	stopReason := "end_turn"
	streamDone := false
	var eventLines []string

	closeBlock := func(bs *blockState) {
		if !bs.open {
			return
		}
		sse.Send(w, flusher, "content_block_stop", map[string]any{
			"type": "content_block_stop",
			"index": bs.idx,
		})
		bs.open = false
	}

	ensureThinkingOpen := func() {
		if thinkingBlock.open {
			return
		}
		sse.Send(w, flusher, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         thinkingBlock.idx,
			"content_block": map[string]any{"type": "thinking", "thinking": ""},
		})
		thinkingBlock.open = true
	}

	ensureTextOpen := func() {
		if textBlock.open {
			return
		}
		closeBlock(&thinkingBlock)
		sse.Send(w, flusher, "content_block_start", map[string]any{
			"type":          "content_block_start",
			"index":         textBlock.idx,
			"content_block": map[string]any{"type": "text", "text": ""},
		})
		textBlock.open = true
	}

	emitToolBlocks := func() {
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
				"type": "content_block_stop",
				"index": idx,
			})
		}
	}

	processChunk := func(choice openai.StreamChunkChoice) {
		for _, tcDelta := range choice.Delta.ToolCalls {
			acc, exists := pending[tcDelta.Index]
			if !exists {
				acc = &openai.ToolCallAccumulator{ID: tcDelta.ID, Name: tcDelta.Function.Name}
				pending[tcDelta.Index] = acc
			}
			acc.Arguments += tcDelta.Function.Arguments
		}

		reasoningText := firstNonEmpty(
			choice.Delta.ReasoningContent,
			choice.Delta.Reasoning,
			choice.Delta.Thinking,
		)
		if reasoningText != "" {
			ensureThinkingOpen()
			outputTokens++
			sse.Send(w, flusher, "content_block_delta", map[string]any{
				"type":  "content_block_delta",
				"index": thinkingBlock.idx,
				"delta": map[string]any{"type": "thinking_delta", "thinking": reasoningText},
			})
		}

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
			p.logger.Info("openai stream finish_reason",
				slog.String("provider", p.name),
				slog.String("model", model),
				slog.String("finish_reason", *choice.FinishReason),
				slog.Int("pending_tools", len(pending)),
				slog.Int("output_tokens_seen", outputTokens),
			)
			if len(pending) > 0 {
				stopReason = "tool_use"
			} else if *choice.FinishReason == "length" {
				stopReason = "max_tokens"
			}
		}
	}

	flushEventLines := func() {
		for _, eventLine := range eventLines {
			if !strings.HasPrefix(eventLine, "data: ") {
				continue
			}
			data := strings.TrimPrefix(eventLine, "data: ")
			if data == "[DONE]" {
				streamDone = true
				continue
			}
			var chunk openai.StreamChunk
			if json.Unmarshal([]byte(data), &chunk) != nil || len(chunk.Choices) == 0 {
				continue
			}
			processChunk(chunk.Choices[0])
		}
		eventLines = eventLines[:0]
	}

	reader := bufio.NewReader(resp.Body)
	for {
		line, err := reader.ReadString('\n')
		if err != nil && len(line) == 0 {
			break
		}

		trimmed := strings.TrimRight(line, "\r\n")
		if trimmed == "" {
			flushEventLines()
			if streamDone {
				break
			}
		} else {
			eventLines = append(eventLines, trimmed)
		}

		if err != nil {
			break
		}
	}

	if len(eventLines) > 0 {
		flushEventLines()
	}

	closeBlock(&thinkingBlock)
	closeBlock(&textBlock)
	emitToolBlocks()

	sse.Send(w, flusher, "message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": stopReason, "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": outputTokens},
	})
	sse.Send(w, flusher, "message_stop", map[string]any{"type": "message_stop"})
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyPtr(values ...*string) string {
	for _, value := range values {
		if value != nil && *value != "" {
			return *value
		}
	}
	return ""
}

func genMessageID() string {
	var buf [8]byte
	_, _ = rand.Read(buf[:])
	return hex.EncodeToString(buf[:])
}
