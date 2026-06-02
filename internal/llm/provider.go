package llm

import (
	"context"

	"encoding/json"
)

type ToolCall struct {
	Id   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type StreamEvent struct {
	Type string    `json:"type"`
	Text string    `json:"text"`
	TC   *ToolCall `json:"tc"`
	Err  error     `json:"error"`
}

type ProviderConfig struct {
	ModelName   string  `json:"model_name"`
	MaxTokens   int     `json:"max_tokens"`
	Temperature float64 `json:"temperature"`
}

type Message struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	TCs     []ToolCall `json:"tcs"`
	TCID    string     `json:"tcid"`
}

type Provider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDef, config ProviderConfig) (<-chan StreamEvent, error)
}
