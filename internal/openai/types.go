package openai

import "encoding/json"

type ChatTemplateKwargs struct {
	EnableThinking bool `json:"enable_thinking,omitempty"`
	ClearThinking  bool `json:"clear_thinking,omitempty"`
}

type Request struct {
	Model              string             `json:"model"`
	Messages           []Message          `json:"messages"`
	MaxTokens          int                `json:"max_tokens,omitempty"`
	Stream             bool               `json:"stream,omitempty"`
	Temperature        *float64           `json:"temperature,omitempty"`
	TopP               *float64           `json:"top_p,omitempty"`
	Stop               []string           `json:"stop,omitempty"`
	Tools              []Tool             `json:"tools,omitempty"`
	ChatTemplateKwargs *ChatTemplateKwargs `json:"chat_template_kwargs,omitempty"`
}

type Message struct {
	Role       string      `json:"role"`
	Content    *string     `json:"content,omitempty"`
	ToolCalls  *[]ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string      `json:"tool_call_id,omitempty"`
	Name       string      `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id,omitempty"`
	Type     string `json:"type"`
	Function FnCall `json:"function"`
}

type FnCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type Tool struct {
	Type     string `json:"type"`
	Function ToolFn `json:"function"`
}

type ToolFn struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type StreamChunkDelta struct {
	Role             string          `json:"role,omitempty"`
	Content          string          `json:"content,omitempty"`
	ReasoningContent string          `json:"reasoning_content,omitempty"`
	Reasoning        string          `json:"reasoning,omitempty"`
	Thinking         string          `json:"thinking,omitempty"`
	ToolCalls        []ToolCallDelta `json:"tool_calls,omitempty"`
}

type StreamChunkChoice struct {
	Index        int              `json:"index"`
	Delta        StreamChunkDelta `json:"delta"`
	FinishReason *string          `json:"finish_reason"`
}

type StreamChunk struct {
	ID      string              `json:"id"`
	Object  string              `json:"object"`
	Model   string              `json:"model,omitempty"`
	Choices []StreamChunkChoice `json:"choices"`
	Usage   *struct {
		PromptTokens     int `json:"prompt_tokens,omitempty"`
		CompletionTokens int `json:"completion_tokens,omitempty"`
		TotalTokens      int `json:"total_tokens,omitempty"`
	} `json:"usage,omitempty"`
}

type ToolCallDelta struct {
	Index    int    `json:"index"`
	ID       string `json:"id,omitempty"`
	Type     string `json:"type,omitempty"`
	Function struct {
		Name      string `json:"name,omitempty"`
		Arguments string `json:"arguments,omitempty"`
	} `json:"function"`
}

type Response struct {
	ID      string `json:"id"`
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Role             string     `json:"role"`
			Content          *string    `json:"content"`
			ReasoningContent *string    `json:"reasoning_content,omitempty"`
			Reasoning        *string    `json:"reasoning,omitempty"`
			Thinking         *string    `json:"thinking,omitempty"`
			ToolCalls        []ToolCall `json:"tool_calls,omitempty"`
		} `json:"message"`
		FinishReason *string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

type ToolCallAccumulator struct {
	ID        string
	Name      string
	Arguments string
}
