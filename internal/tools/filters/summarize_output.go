package filters

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/AamindMandragora/pragma/internal/llm"
)

type SummarizeOutputFilter struct {
	Model *llm.Model
}

func (f *SummarizeOutputFilter) Name() string { return "summarize_output" }
func (f *SummarizeOutputFilter) Description() string {
	return "Output filter: privately summarizes complete tool output, preserving important errors, names, behavior, and actionable details."
}
func (f *SummarizeOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"focus":{"type":"string","description":"Optional aspect to focus on."}`)
}
func (f *SummarizeOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *SummarizeOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	var params struct {
		Focus string `json:"focus,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", errors.New("cannot summarize empty output")
	}
	if f.Model == nil || f.Model.Provider == nil {
		return "", errors.New("summarize_output has no model provider")
	}
	return f.summarize(context.Background(), text, params.Focus)
}

func (f *SummarizeOutputFilter) summarize(ctx context.Context, text, focus string) (string, error) {
	chunks := splitSummaryChunks(text, maxSummaryChunkChars)
	partial := make([]string, 0, len(chunks))
	for _, chunk := range chunks {
		result, err := f.summarizeChunk(ctx, chunk, focus)
		if err != nil {
			return "", err
		}
		partial = append(partial, result)
	}
	if len(partial) == 1 {
		return partial[0], nil
	}

	combined := strings.Join(partial, "\n\n---\n\n")
	return f.summarizeChunk(ctx, combined, focus+" Combine the partial summaries into one concise result.")
}

func (f *SummarizeOutputFilter) summarizeChunk(ctx context.Context, text, focus string) (string, error) {
	model := *f.Model
	model.MaxTokens = defaultSummaryMaxTokens
	model.Effort = ""

	prompt := "Summarize the supplied text for a coding agent. Preserve important names, behavior, errors, interfaces, and actionable details. Do not reproduce the full source. Keep the result concise."
	if focus != "" {
		prompt += " Focus on: " + focus
	}
	prompt += "\n\nTEXT:\n" + text

	ch, err := model.Provider.Chat(ctx, []llm.Message{
		{Role: "system", Content: prompt},
		{Role: "user", Content: "Return only the summary."},
	}, nil, model)
	if err != nil {
		return "", err
	}

	var result strings.Builder
	for event := range ch {
		switch event.Type {
		case "text":
			result.WriteString(event.Text)
		case "error":
			return "", event.Err
		}
	}
	if strings.TrimSpace(result.String()) == "" {
		return "", errors.New("summarizer returned no visible response")
	}
	return strings.TrimSpace(result.String()), nil
}

func splitSummaryChunks(text string, maxChars int) []string {
	if len(text) <= maxChars {
		return []string{text}
	}

	var chunks []string
	for len(text) > maxChars {
		cut := strings.LastIndexAny(text[:maxChars], "\n ")
		if cut < maxChars/2 {
			cut = maxChars
		}
		chunks = append(chunks, text[:cut])
		text = strings.TrimLeft(text[cut:], " \n")
	}
	if text != "" {
		chunks = append(chunks, text)
	}
	return chunks
}
