package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type AnthropicProvider struct {
	BaseProvider
}

type anthropicRequest struct {
	Model       string                   `json:"model"`
	MaxTokens   int                      `json:"max_tokens"`
	System      string                   `json:"system,omitempty"`
	Messages    []map[string]interface{} `json:"messages"`
	Stream      bool                     `json:"stream"`
	Tools       []map[string]interface{} `json:"tools,omitempty"`
	Temperature float64                  `json:"temperature"`
}

type anthropicChunk struct {
	Type  string `json:"type"`
	Index int    `json:"index"`

	Message *struct {
		Usage *struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	} `json:"message"`

	ContentBlock *struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`

	Delta *struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`

	Usage *struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

func (a *AnthropicProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, model Model) (<-chan StreamEvent, error) {
	systemPrompt, apiMessages := a.splitSystem(messages)

	reqBody := anthropicRequest{
		Model:       model.Name,
		MaxTokens:   model.MaxTokens,
		System:      systemPrompt,
		Messages:    apiMessages,
		Stream:      true,
		Temperature: model.Temperature,
	}
	if len(tools) > 0 {
		reqBody.Tools = a.toAPITools(tools)
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", "https://api.anthropic.com/v1/messages", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", a.APIKey)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("anthropic-version", "2023-06-01")

	ch := make(chan StreamEvent)
	go func() {
		defer close(ch)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- StreamEvent{Type: "error", Err: err}
			return
		}
		defer res.Body.Close()
		if res.StatusCode != http.StatusOK {
			buf := new(bytes.Buffer)
			buf.ReadFrom(res.Body)
			ch <- StreamEvent{
				Type: "error",
				Err:  fmt.Errorf("anthropic api returned status %d: %s", res.StatusCode, buf.String()),
			}
			return
		}

		scanner := bufio.NewScanner(res.Body)
		var inputTokens int
		var outputTokens int

		type toolCallInProgress struct {
			id   string
			name string
			args strings.Builder
		}
		activeTools := map[int]*toolCallInProgress{}

		for scanner.Scan() {
			line := scanner.Text()
			if line == "" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")

			var event anthropicChunk
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}

			switch event.Type {
			case "message_start":
				if event.Message != nil && event.Message.Usage != nil {
					inputTokens = event.Message.Usage.InputTokens
				}

			case "content_block_start":
				if event.ContentBlock != nil && event.ContentBlock.Type == "tool_use" {
					activeTools[event.Index] = &toolCallInProgress{
						id:   event.ContentBlock.ID,
						name: event.ContentBlock.Name,
					}
				}

			case "content_block_delta":
				if event.Delta == nil {
					continue
				}
				switch event.Delta.Type {
				case "text_delta":
					if event.Delta.Text != "" {
						ch <- StreamEvent{Type: "text", Text: event.Delta.Text}
					}
				case "input_json_delta":
					if tc, ok := activeTools[event.Index]; ok {
						tc.args.WriteString(event.Delta.PartialJSON)
					}
				}

			case "content_block_stop":
				if tc, ok := activeTools[event.Index]; ok {
					ch <- StreamEvent{
						Type: "tool_call",
						TC: &ToolCall{
							Id:   tc.id,
							Name: tc.name,
							Args: json.RawMessage(tc.args.String()),
						},
					}
					delete(activeTools, event.Index)
				}

			case "message_delta":
				if event.Usage != nil {
					outputTokens = event.Usage.OutputTokens
				}

			case "message_stop":
				ch <- StreamEvent{
					Type: "usage",
					Usage: &TokenUsage{
						InputTokens:  inputTokens,
						OutputTokens: outputTokens,
						Model:        model.Name,
					},
				}
				ch <- StreamEvent{Type: "done"}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Type: "error", Err: err}
		}
	}()
	return ch, nil
}

func (a *AnthropicProvider) splitSystem(messages []Message) (string, []map[string]interface{}) {
	var system string
	var msgs []map[string]interface{}
	for _, msg := range messages {
		if msg.Role == "system" {
			system = msg.Content
			continue
		}
		switch msg.Role {
		case "tool":
			msgs = append(msgs, map[string]interface{}{
				"role": "user",
				"content": []map[string]interface{}{
					{
						"type":        "tool_result",
						"tool_use_id": msg.TCID,
						"content":     msg.Content,
					},
				},
			})
		case "assistant":
			if len(msg.TCs) > 0 {
				var content []map[string]interface{}
				if msg.Content != "" {
					content = append(content, map[string]interface{}{
						"type": "text",
						"text": msg.Content,
					})
				}
				for _, tc := range msg.TCs {
					var parsedArgs interface{}
					json.Unmarshal(tc.Args, &parsedArgs)
					content = append(content, map[string]interface{}{
						"type":  "tool_use",
						"id":    tc.Id,
						"name":  tc.Name,
						"input": parsedArgs,
					})
				}
				msgs = append(msgs, map[string]interface{}{
					"role":    "assistant",
					"content": content,
				})
			} else {
				msgs = append(msgs, map[string]interface{}{
					"role":    "assistant",
					"content": msg.Content,
				})
			}
		default:
			msgs = append(msgs, map[string]interface{}{
				"role":    msg.Role,
				"content": msg.Content,
			})
		}
	}
	return system, msgs
}

func (a *AnthropicProvider) toAPITools(tools []ToolDef) []map[string]interface{} {
	var t []map[string]interface{}
	for _, tool := range tools {
		var schema interface{}
		json.Unmarshal(tool.InputSchema, &schema)
		t = append(t, map[string]interface{}{
			"name":         tool.Name,
			"description":  tool.Description,
			"input_schema": schema,
		})
	}
	return t
}
