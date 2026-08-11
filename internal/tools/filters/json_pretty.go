package filters

import (
	"bytes"
	"encoding/json"
	"fmt"
)

type JSONPrettyFilter struct{}

func (f *JSONPrettyFilter) Name() string { return "json_pretty" }
func (f *JSONPrettyFilter) Description() string {
	return "Output filter: validates and pretty-prints JSON output for easier inspection."
}
func (f *JSONPrettyFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"indent":{"type":"string","default":"  ","description":"Indentation string."}`)
}
func (f *JSONPrettyFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *JSONPrettyFilter) Apply(text string, args json.RawMessage) (string, error) {
	indent := "  "
	var params struct {
		Indent string `json:"indent,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Indent != "" {
		indent = params.Indent
	}
	var result bytes.Buffer
	if err := json.Indent(&result, []byte(text), "", indent); err != nil {
		return "", fmt.Errorf("invalid JSON: %w", err)
	}
	return result.String(), nil
}
