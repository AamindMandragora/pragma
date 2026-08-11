package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"

	"github.com/AamindMandragora/pragma/internal/llm/catalog"
)

const maxStreamLineSize = 16 * 1024 * 1024

// NewStreamScanner handles SSE events that contain large tool arguments or
// text deltas. bufio.Scanner's default token limit is only 64 KiB.
func NewStreamScanner(reader io.Reader) *bufio.Scanner {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), maxStreamLineSize)
	return scanner
}

// holds the id, name of tool, and args that the llm called with
type ToolCall struct {
	Id   string          `json:"id"`
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
}

// holds the name, desc, and input schema of a tool
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// tracks the number of input/output tokens from/to a model
type TokenUsage struct {
	InputTokens       int
	CachedInputTokens int
	OutputTokens      int
	Model             string
}

// this is the packet the model actually returns
type StreamEvent struct {
	Type  string      `json:"type"`  // tells us which of the next four fields to use
	Text  string      `json:"text"`  // regular message
	TC    *ToolCall   `json:"tc"`    // tool call
	Err   error       `json:"error"` // error
	Usage *TokenUsage `json:"usage"` // cost usage info
}

// holds the sender of the message (system, user, assistant, etc)
type Message struct {
	Role    string     `json:"role"`
	Content string     `json:"content"`
	TCs     []ToolCall `json:"tcs"`
	TCID    string     `json:"tcid"`
}

// defines a chatprovider as a struct that implements the Chat and GetName functions
type ChatProvider interface {
	Chat(ctx context.Context, messages []Message, tools []ToolDef, model Model) (<-chan StreamEvent, error) // sends a history of messages to the model through the provider, as well as a list of tool definitions
	GetName() string
}

// all providers will inherit this struct and will therefore have a name and apikey
type BaseProvider struct {
	Name   string
	APIKey string
}

// getter for the provider name
func (b *BaseProvider) GetName() string {
	return b.Name
}

// the non-config usable model struct
type Model struct {
	Name        string
	MaxTokens   int
	Temperature float64
	Effort      string
	Provider    ChatProvider
	ToolMode    string
}

// our model tier here will hold a model and the min budget used percent threshold
type ModelTier struct {
	Model     *Model
	Threshold float64
}

// will return a chatprovider by attempting to read the apikey and using the catalog-resolved provider
func MakeProvider(providerName string, apiKeyVar string) ChatProvider {
	resolved, err := catalog.ResolveProvider(providerName)
	if err != nil {
		// unknown providers fall back to openrouter-compatible defaults when present
		if fallback, ferr := catalog.ResolveProvider("openrouter"); ferr == nil {
			resolved = fallback
			resolved.Name = providerName
		} else {
			resolved = catalog.ResolvedProvider{
				Name:         providerName,
				Protocol:     "openai",
				BaseURL:      "https://openrouter.ai/api/v1",
				EndpointPath: "responses",
				AuthHeader:   "Authorization",
				AuthPrefix:   "Bearer ",
				Headers:      map[string]string{"Content-Type": "application/json"},
				ToolMode:     "text",
			}
		}
	}
	keyVar := apiKeyVar
	if keyVar == "" {
		keyVar = resolved.APIKeyVar
	}
	key := readKey(keyVar)
	base := BaseProvider{Name: providerName, APIKey: key}
	switch resolved.Protocol {
	case "anthropic":
		return &AnthropicProvider{BaseProvider: base, Config: resolved}
	default:
		return &OpenAIProvider{BaseProvider: base, Config: resolved}
	}
}

// openai and anthropic models support native tool calls, but we'll assume text in other cases
func ToolModeForProvider(provider string) string {
	resolved, err := catalog.ResolveProvider(provider)
	if err != nil {
		return "text"
	}
	if resolved.ToolMode != "" {
		return resolved.ToolMode
	}
	return "text"
}

// function to get the value for a environment variable
func readKey(varName string) string {
	// tries .agent/.env first
	data, err := os.ReadFile(".agent/.env")
	// then tries local
	if err != nil {
		data, err = os.ReadFile(".env")
	}
	if err == nil {
		// reads each line in the .env, che
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			// if line is empty or comment ignore
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			// if line's variable is varName return the value
			if strings.HasPrefix(line, varName+"=") {
				parts := strings.SplitN(line, "=", 2)
				return strings.Trim(parts[1], `"' `)
			}
		}
	}
	// attempt to read global ENV as a fallback
	return os.Getenv(varName)
}
