package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/AamindMandragora/pragma/internal/llm/catalog"
)

// this provider can be used for openai and openrouter, which is why we need to hold the resolved catalog config
type OpenAIProvider struct {
	BaseProvider
	Config catalog.ResolvedProvider
}

// streaming event from the Responses API (typed SSE payloads)
type openAIStreamEvent struct {
	Type string `json:"type"`

	Delta string `json:"delta"`
	Text  string `json:"text"`

	// present on response.output_item.added / .done
	Item *struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Status  string `json:"status"`
		CallID  string `json:"call_id"`
		Name    string `json:"name"`
		Args    string `json:"arguments"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	} `json:"item"`

	OutputIndex int `json:"output_index"`

	// present on response.function_call_arguments.done
	CallID    string `json:"call_id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`

	// present on response.completed / response.failed
	Response *struct {
		Model  string `json:"model"`
		Status string `json:"status"`
		Error  *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
		Usage *struct {
			InputTokens        int `json:"input_tokens"`
			OutputTokens       int `json:"output_tokens"`
			InputTokensDetails *struct {
				CachedTokens int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
		IncompleteDetails *struct {
			Reason string `json:"reason"`
		} `json:"incomplete_details"`
	} `json:"response"`

	// present on top-level error events
	Code    string `json:"code"`
	Message string `json:"message"`
}

// sends the message history and tool definitions to the model through provider, receives a StreamEvent
func (o *OpenAIProvider) Chat(ctx context.Context, messages []Message, tools []ToolDef, model Model) (<-chan StreamEvent, error) {
	instructions, input := o.toAPIInput(messages)
	logical := map[string]interface{}{
		"model":        model.Name,
		"input":        input,
		"stream":       true,
		"max_tokens":   model.MaxTokens,
		"temperature":  model.Temperature,
		"instructions": instructions,
	}
	if model.Effort != "" {
		logical["reasoning"] = map[string]string{
			"effort": model.Effort,
		}
	}
	if len(tools) > 0 {
		logical["tools"] = o.toAPITools(tools)
	}
	bodyMap := o.Config.BuildBody(logical)
	body, err := json.Marshal(bodyMap)
	if err != nil {
		return nil, err
	}
	u, err := url.Parse(o.Config.BaseURL)
	if err != nil {
		return nil, err
	}
	apiURL := u.JoinPath(o.Config.EndpointPath).String()
	req, err := http.NewRequestWithContext(ctx, "POST", apiURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	o.applyHeaders(req)
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
				Err:  fmt.Errorf("%s returned status %d: %s", o.Config.BaseURL, res.StatusCode, buf.String()),
			}
			return
		}
		scanner := NewStreamScanner(res.Body)
		type toolCallInProgress struct {
			id   string
			name string
			args strings.Builder
		}
		toolCalls := map[int]*toolCallInProgress{}
		evText := o.Config.Event("text_delta")
		evTextDone := o.Config.Event("text_done")
		evToolAdded := o.Config.Event("tool_item_added")
		evArgsDelta := o.Config.Event("tool_args_delta")
		evArgsDone := o.Config.Event("tool_args_done")
		evCompleted := o.Config.Event("completed")
		evFailed := o.Config.Event("failed")
		evError := o.Config.Event("error")
		terminal := false
		textReceived := false
		for scanner.Scan() {
			line := scanner.Text()
			if line == "" || line == "data: [DONE]" {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}
			data := strings.TrimPrefix(line, "data: ")
			var event openAIStreamEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			switch event.Type {
			case evText:
				if event.Delta != "" {
					textReceived = true
					ch <- StreamEvent{Type: "text", Text: event.Delta}
				}
			case evTextDone:
				// Some OpenAI-compatible endpoints send only the completed text
				// event. Avoid duplicating it when deltas were already received.
				if event.Text != "" && !textReceived {
					ch <- StreamEvent{Type: "text", Text: event.Text}
				}
			case evToolAdded:
				if event.Item != nil && event.Item.Type == "function_call" {
					toolCalls[event.OutputIndex] = &toolCallInProgress{
						id:   event.Item.CallID,
						name: event.Item.Name,
					}
				}
			case evArgsDelta:
				if tc, ok := toolCalls[event.OutputIndex]; ok {
					tc.args.WriteString(event.Delta)
				}
			case evArgsDone:
				tc, ok := toolCalls[event.OutputIndex]
				if !ok {
					tc = &toolCallInProgress{}
					toolCalls[event.OutputIndex] = tc
				}
				if event.CallID != "" {
					tc.id = event.CallID
				}
				if event.Name != "" {
					tc.name = event.Name
				}
				if event.Arguments != "" {
					tc.args.Reset()
					tc.args.WriteString(event.Arguments)
				}
			case evCompleted:
				terminal = true
				if event.Response != nil && event.Response.Status != "" && event.Response.Status != "completed" {
					reason := event.Response.Status
					if event.Response.IncompleteDetails != nil && event.Response.IncompleteDetails.Reason != "" {
						reason = event.Response.IncompleteDetails.Reason
					}
					ch <- StreamEvent{Type: "error", Err: fmt.Errorf("model response incomplete (%s); context or output limits may have been reached", reason)}
					return
				}
				if event.Response != nil && event.Response.Usage != nil {
					usageModel := event.Response.Model
					if usageModel == "" {
						usageModel = model.Name
					}
					cached := 0
					if event.Response.Usage.InputTokensDetails != nil {
						cached = event.Response.Usage.InputTokensDetails.CachedTokens
					}
					ch <- StreamEvent{
						Type: "usage",
						Usage: &TokenUsage{
							InputTokens:       event.Response.Usage.InputTokens,
							CachedInputTokens: cached,
							OutputTokens:      event.Response.Usage.OutputTokens,
							Model:             usageModel,
						},
					}
				}
			case evFailed:
				terminal = true
				msg := "response failed"
				if event.Response != nil && event.Response.Error != nil && event.Response.Error.Message != "" {
					msg = event.Response.Error.Message
				}
				ch <- StreamEvent{Type: "error", Err: fmt.Errorf("%s", msg)}
				return
			case evError:
				terminal = true
				msg := event.Message
				if msg == "" {
					msg = "stream error"
				}
				ch <- StreamEvent{Type: "error", Err: fmt.Errorf("%s", msg)}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamEvent{Type: "error", Err: fmt.Errorf("OpenAI response stream: %w", err)}
			return
		}
		if !terminal {
			ch <- StreamEvent{Type: "error", Err: fmt.Errorf("model stream ended before completion; context or output limits may have been reached")}
			return
		}
		maxIdx := -1
		for idx := range toolCalls {
			if idx > maxIdx {
				maxIdx = idx
			}
		}
		for i := 0; i <= maxIdx; i++ {
			tc, ok := toolCalls[i]
			if !ok {
				continue
			}
			args := tc.args.String()
			if args == "" {
				args = "{}"
			}
			if !json.Valid([]byte(args)) {
				ch <- StreamEvent{Type: "error", Err: fmt.Errorf("model returned incomplete arguments for %s", tc.name)}
				return
			}
			ch <- StreamEvent{
				Type: "tool_call",
				TC: &ToolCall{
					Id:   tc.id,
					Name: tc.name,
					Args: json.RawMessage(args),
				},
			}
		}
		ch <- StreamEvent{Type: "done"}
	}()
	return ch, nil
}

func (o *OpenAIProvider) applyHeaders(req *http.Request) {
	for k, v := range o.Config.Headers {
		req.Header.Set(k, v)
	}
	if o.Config.AuthHeader != "" {
		req.Header.Set(o.Config.AuthHeader, o.Config.AuthPrefix+o.APIKey)
	}
}

// converts our internal message history to Responses API instructions + input items
func (o *OpenAIProvider) toAPIInput(messages []Message) (string, []map[string]interface{}) {
	var instructions string
	var input []map[string]interface{}
	for _, message := range messages {
		switch message.Role {
		case "system":
			if len(input) == 0 && instructions == "" {
				instructions = message.Content
			} else if len(input) == 0 {
				instructions = instructions + "\n\n" + message.Content
			} else {
				input = append(input, map[string]interface{}{"role": "developer", "content": message.Content})
			}
		case "tool":
			input = append(input, map[string]interface{}{
				"type":    "function_call_output",
				"call_id": message.TCID,
				"output":  message.Content,
			})
		case "assistant":
			if message.Content != "" {
				input = append(input, map[string]interface{}{"role": "assistant", "content": message.Content})
			}
			for _, tc := range message.TCs {
				args := string(tc.Args)
				if args == "" {
					args = "{}"
				}
				input = append(input, map[string]interface{}{
					"type":      "function_call",
					"call_id":   tc.Id,
					"name":      tc.Name,
					"arguments": args,
				})
			}
		default:
			input = append(input, map[string]interface{}{"role": message.Role, "content": message.Content})
		}
	}
	return instructions, input
}

// converts the internal tool representation to what the Responses API expects
func (o *OpenAIProvider) toAPITools(tools []ToolDef) []map[string]interface{} {
	var m []map[string]interface{}
	for _, tool := range tools {
		var params interface{}
		if len(tool.InputSchema) > 0 {
			_ = json.Unmarshal(tool.InputSchema, &params)
		}
		m = append(m, map[string]interface{}{
			"type":        "function",
			"name":        tool.Name,
			"description": tool.Description,
			"parameters":  params,
		})
	}
	return m
}
