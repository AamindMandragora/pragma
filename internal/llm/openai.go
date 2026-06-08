package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

type OpenAIProvider struct {
	BaseProvider
	BaseURL string
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Model string `json:"model"`
}

func (o *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, model Model) (<-chan StreamEvent, error) {
	bodyMap := map[string]interface{}{
		"model":                 model.Name,
		"messages":              o.toAPIMessages(messages),
		"stream":                true,
		"max_completion_tokens": model.MaxTokens,
		"temperature":           model.Temperature,
		"stream_options":        map[string]interface{}{"include_usage": true},
	}
	if len(tools) > 0 {
		bodyMap["tools"] = o.toAPITools(tools)
	}
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(o.BaseURL)
	if err != nil {
		return nil, err
	}
	apiURL := u.JoinPath("chat/completions").String()
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
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
				Err:  fmt.Errorf("openrouter api returned status %d: %s", res.StatusCode, buf.String()),
			}
			return
		}
		scanner := bufio.NewScanner(res.Body)
		toolCalls := map[int]*ToolCall{}
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || line == "data: [DONE]" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var chunk openAIChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			if chunk.Usage != nil {
				ch <- StreamEvent{Type: "usage", Usage: &TokenUsage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, Model: chunk.Model}}
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			if chunk.Choices[0].Delta.Content != "" {
				ch <- StreamEvent{Type: "text", Text: chunk.Choices[0].Delta.Content}
			}
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				existing, ok := toolCalls[tc.Index]
				if !ok {
					toolCalls[tc.Index] = &ToolCall{Id: tc.ID, Name: tc.Function.Name, Args: json.RawMessage(tc.Function.Arguments)}
				} else {
					existing.Args = json.RawMessage(string(existing.Args) + tc.Function.Arguments)
				}
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Type: "error", Err: err}
			return
		}
		for _, tc := range toolCalls {
			ch <- StreamEvent{Type: "tool_call", TC: tc}
		}
		ch <- StreamEvent{Type: "done"}
	}()
	return ch, nil
}

func (o *OpenAIProvider) toAPIMessages(messages []Message) []map[string]interface{} {
	var m []map[string]interface{}
	for _, message := range messages {
		switch message.Role {
		case "tool":
			m = append(m, map[string]interface{}{"role": "tool", "tool_call_id": message.TCID, "content": message.Content})
		case "assistant":
			var tcs []map[string]interface{}
			for _, tc := range message.TCs {
				tcs = append(tcs, map[string]interface{}{"id": tc.Id, "type": "function", "function": map[string]interface{}{"name": tc.Name, "arguments": string(tc.Args)}})
			}
			m = append(m, map[string]interface{}{"role": "assistant", "content": message.Content, "tool_calls": tcs})
		default:
			m = append(m, map[string]interface{}{"role": message.Role, "content": message.Content})
		}
	}
	return m
}

func (o *OpenAIProvider) toAPITools(tools []ToolDef) []map[string]interface{} {
	var m []map[string]interface{}
	for _, tool := range tools {
		m = append(m, map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema}})
	}
	return m
}
