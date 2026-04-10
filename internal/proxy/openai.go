package proxy

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"opencode-proxy/internal/anthropic"
	"opencode-proxy/internal/config"
	"opencode-proxy/internal/convert"
	"opencode-proxy/internal/openai"
	"opencode-proxy/internal/sse"
)

// tryOpenAIProxy, Anthropic isteğini OpenAI formatına çevirip ileten sağlayıcıdır.
func tryOpenAIProxy(w http.ResponseWriter, r *http.Request, p config.Provider, antReq anthropic.Request) (bool, string) {
	oaiReq := convert.ToOpenAI(antReq)
	oaiBody, err := json.Marshal(oaiReq)
	if err != nil {
		return false, err.Error()
	}

	proxyReq, err := http.NewRequestWithContext(r.Context(), http.MethodPost, p.BaseURL, strings.NewReader(string(oaiBody)))
	if err != nil {
		return false, err.Error()
	}
	proxyReq.Header.Set("Content-Type", "application/json")
	proxyReq.Header.Set("Authorization", "Bearer "+p.APIKey)

	client := &http.Client{Timeout: 300 * time.Second}
	resp, err := client.Do(proxyReq)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errMsg := fmtError(resp)
		log.Printf("❌ %s başarısız: %s → sonrakine geçiliyor", p.Name, errMsg)

		if resp.StatusCode == http.StatusTooManyRequests ||
			resp.StatusCode == http.StatusUnauthorized ||
			resp.StatusCode == http.StatusForbidden ||
			resp.StatusCode == http.StatusPaymentRequired {
			return false, errMsg
		}
		return false, errMsg
	}

	log.Printf("✅ %s başarılı (openai proxy)", p.Name)

	if antReq.Stream {
		streamResponse(w, resp, antReq.Model)
	} else {
		nonStreamResponse(w, resp, antReq.Model)
	}
	return true, ""
}

func fmtError(resp *http.Response) string {
	body, _ := io.ReadAll(resp.Body)
	return fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body))
}

func nonStreamResponse(w http.ResponseWriter, resp *http.Response, model string) {
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

	var content []interface{}
	stopReason := "end_turn"

	if len(oaiResp.Choices) > 0 {
		choice := oaiResp.Choices[0]
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

	respBody, _ := json.Marshal(map[string]interface{}{
		"id":            "msg-" + oaiResp.ID,
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":  oaiResp.Usage.PromptTokens,
			"output_tokens": oaiResp.Usage.CompletionTokens,
		},
	})
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("anthropic-version", "2023-06-01")
	w.Write(respBody)
}

func streamResponse(w http.ResponseWriter, resp *http.Response, model string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		sse.WriteError(w, http.StatusInternalServerError, "streaming desteklenmiyor")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("anthropic-version", "2023-06-01")

	msgID := "msg-proxy-" + genID()

	sse.Send(w, flusher, "message_start", map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id": msgID, "type": "message", "role": "assistant",
			"content": []interface{}{}, "model": model,
			"stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]interface{}{"input_tokens": 0, "output_tokens": 0},
		},
	})

	textBlockIdx := 0
	sse.Send(w, flusher, "content_block_start", map[string]interface{}{
		"type":          "content_block_start",
		"index":         textBlockIdx,
		"content_block": map[string]interface{}{"type": "text", "text": ""},
	})

	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	outputTokens := 0
	pending := make(map[int]*openai.ToolCallAccumulator)

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

		if choice.Delta.Content != "" {
			outputTokens++
			sse.Send(w, flusher, "content_block_delta", map[string]interface{}{
				"type":  "content_block_delta",
				"index": textBlockIdx,
				"delta": map[string]interface{}{"type": "text_delta", "text": choice.Delta.Content},
			})
		}

		if choice.FinishReason != nil {
			sse.Send(w, flusher, "content_block_stop", map[string]interface{}{
				"type": "content_block_stop", "index": textBlockIdx,
			})

			for i := 0; i < len(pending); i++ {
				tc := pending[i]
				args := tc.Arguments
				if args == "" {
					args = "{}"
				}
				idx := textBlockIdx + 1 + i
				sse.Send(w, flusher, "content_block_start", map[string]interface{}{
					"type":  "content_block_start",
					"index": idx,
					"content_block": map[string]interface{}{
						"type": "tool_use", "id": tc.ID, "name": tc.Name,
						"input": json.RawMessage(args),
					},
				})
				sse.Send(w, flusher, "content_block_stop", map[string]interface{}{
					"type": "content_block_stop", "index": idx,
				})
			}

			stopReason := "end_turn"
			if len(pending) > 0 {
				stopReason = "tool_use"
			} else if *choice.FinishReason == "length" {
				stopReason = "max_tokens"
			}

			sse.Send(w, flusher, "message_delta", map[string]interface{}{
				"type":  "message_delta",
				"delta": map[string]interface{}{"stop_reason": stopReason, "stop_sequence": nil},
				"usage": map[string]interface{}{"output_tokens": outputTokens},
			})
			sse.Send(w, flusher, "message_stop", map[string]interface{}{"type": "message_stop"})
		}
	}
}

func genID() string {
	b := make([]byte, 12)
	for i := range b {
		b[i] = "abcdefghijklmnopqrstuvwxyz0123456789"[time.Now().UnixNano()%36]
		time.Sleep(time.Nanosecond)
	}
	return string(b)
}
