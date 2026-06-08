package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
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

type TokenUsage struct {
	InputTokens  int
	OutputTokens int
	Model        string
}

type StreamEvent struct {
	Type  string      `json:"type"`
	Text  string      `json:"text"`
	TC    *ToolCall   `json:"tc"`
	Err   error       `json:"error"`
	Usage *TokenUsage `json:"usage"`
}

type Message struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	TCs     []ToolCall `json:"tcs"`
	TCID    string     `json:"tcid"`
}

type ChatProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDef, model Model) (<-chan StreamEvent, error)
	GetName() string
}

type BaseProvider struct {
	Name   string
	APIKey string
}

func (b *BaseProvider) GetName() string {
	return b.Name
}

type Model struct {
	Name        string
	MaxTokens   int
	Temperature float64
	Provider    ChatProvider
	ToolMode    string
}

type ModelTier struct {
	Model     *Model
	Threshold float64
}

func MakeProvider(providerName string, apiKeyVar string) ChatProvider {
	key := readKey(apiKeyVar)
	base := BaseProvider{Name: providerName, APIKey: key}
	switch providerName {
	case "anthropic":
		return &AnthropicProvider{BaseProvider: base}
	case "openai":
		return &OpenAIProvider{BaseProvider: base, BaseURL: "https://api.openai.com/v1"}
	case "openrouter":
		return &OpenAIProvider{BaseProvider: base, BaseURL: "https://openrouter.ai/api/v1"}
	default:
		return &OpenAIProvider{BaseProvider: base, BaseURL: "https://openrouter.ai/api/v1"}
	}
}

func ToolModeForProvider(provider string) string {
	switch provider {
	case "openai", "anthropic":
		return "native"
	default:
		return "text"
	}
}

func readKey(varName string) string {
	data, err := os.ReadFile(".env")
	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, varName+"=") {
				parts := strings.SplitN(line, "=", 2)
				return strings.Trim(parts[1], `"' `)
			}
		}
	}
	return os.Getenv(varName)
}