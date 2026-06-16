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

// this provider can be used for openai and openrouter, which is why we need to hold the base url
type OpenAIProvider struct {
	BaseProvider
	BaseURL string
}

// the chunk that openai will return, to be converted to our internal format
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

// sends the message history and tool definitions to the model through provider, receives a StreamEvent
func (o *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, model Model) (<-chan StreamEvent, error) {
	// creates the map for the body of our request
	bodyMap := map[string]interface{}{
		"model":                 model.Name,
		"messages":              o.toAPIMessages(messages),
		"stream":                true,
		"max_completion_tokens": model.MaxTokens,
		"temperature":           model.Temperature,
		"stream_options":        map[string]interface{}{"include_usage": true},
	}
	// adds ToolDefs to the body if we're using any
	if len(tools) > 0 {
		bodyMap["tools"] = o.toAPITools(tools)
	}
	// encodes bodyMap into json
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	// converts the url string ot a url object
	u, err := url.Parse(o.BaseURL)
	if err != nil {
		return nil, err
	}
	// creates the api url by adding chat/completions to the end
	apiURL := u.JoinPath("chat/completions").String()
	// creates http request to the api url, creates and passes in an io.Reader for the body, uses context to control the request and prevent leaks
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	// gives the request a header holding the auth key and the content type
	req.Header.Set("Authorization", "Bearer "+o.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// creates a channel (go version of pipe) for the caller to read StreamEvents from
	ch := make(chan StreamEvent)
	// starts a goroutine
	go func() {
		// ensures that no matter what the channel will get closed at the end of the goroutine (will become read-only since there's a reader)
		defer close(ch)
		// sends the http request and any error that happens
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			ch <- StreamEvent{Type: "error", Err: err}
			return
		}
		// ensures that the io.Reader for the body gets closed at the end of the goroutine
		defer res.Body.Close()
		// if http status is non-ok then send an error event
		if res.StatusCode != http.StatusOK {
			buf := new(bytes.Buffer)
			buf.ReadFrom(res.Body)
			ch <- StreamEvent{
				Type: "error",
				Err:  fmt.Errorf("%s returned status %d: %s", o.BaseURL, res.StatusCode, buf.String()),
			}
			return
		}
		scanner := bufio.NewScanner(res.Body)
		toolCalls := map[int]*ToolCall{}
		// reads each line of the response body
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || line == "data: [DONE]" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			// gets the value of the data field
			data := strings.TrimPrefix(line, "data: ")
			// attempts to fill in the chunk with that string
			var chunk openAIChunk
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue
			}
			// if chunk holds usage info then send that event
			if chunk.Usage != nil {
				ch <- StreamEvent{Type: "usage", Usage: &TokenUsage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens, Model: chunk.Model}}
			}
			// if chunk held no choices for response then continue
			if len(chunk.Choices) == 0 {
				continue
			}
			// we'll always use the first choice, send the content through a StreamEvent
			if chunk.Choices[0].Delta.Content != "" {
				ch <- StreamEvent{Type: "text", Text: chunk.Choices[0].Delta.Content}
			}
			// loop through all the tool calls
			for _, tc := range chunk.Choices[0].Delta.ToolCalls {
				// if we already have this tool call stored then append the arguments, otherwise create a new tool call and store it in our map
				existing, ok := toolCalls[tc.Index]
				if !ok {
					toolCalls[tc.Index] = &ToolCall{Id: tc.ID, Name: tc.Function.Name, Args: json.RawMessage(tc.Function.Arguments)}
				} else {
					existing.Args = json.RawMessage(string(existing.Args) + tc.Function.Arguments)
				}
			}
		}
		// check if the scanner failed, return an error if so
		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Type: "error", Err: err}
			return
		}
		// send tool call events through the channel
		for _, tc := range toolCalls {
			ch <- StreamEvent{Type: "tool_call", TC: tc}
		}
		// send done event to signify no more messages
		ch <- StreamEvent{Type: "done"}
	}()
	// return the channel (now read-only) and a nil error
	return ch, nil
}

// converts our internal message storage to what openai expects
func (o *OpenAIProvider) toAPIMessages(messages []Message) []map[string]interface{} {
	var m []map[string]interface{}
	// loops through each message, converts format, and adds to the map
	for _, message := range messages {
		switch message.Role {
		case "tool":
			// adds the result of a tool call
			m = append(m, map[string]interface{}{"role": "tool", "tool_call_id": message.TCID, "content": message.Content})
		case "assistant":
			// adds tool calls and messages made by the assistant
			var tcs []map[string]interface{}
			for _, tc := range message.TCs {
				tcs = append(tcs, map[string]interface{}{"id": tc.Id, "type": "function", "function": map[string]interface{}{"name": tc.Name, "arguments": string(tc.Args)}})
			}
			m = append(m, map[string]interface{}{"role": "assistant", "content": message.Content, "tool_calls": tcs})
		default:
			// for other cases role and content will suffice
			m = append(m, map[string]interface{}{"role": message.Role, "content": message.Content})
		}
	}
	return m
}

// converts the internal tool representation to what openai expects
func (o *OpenAIProvider) toAPITools(tools []ToolDef) []map[string]interface{} {
	var m []map[string]interface{}
	for _, tool := range tools {
		m = append(m, map[string]interface{}{"type": "function", "function": map[string]interface{}{"name": tool.Name, "description": tool.Description, "parameters": tool.InputSchema}})
	}
	return m
}
