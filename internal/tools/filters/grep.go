package filters

import (
	"encoding/json"
	"errors"
)

type GrepOutputFilter struct{}

func (f *GrepOutputFilter) Name() string { return "grep" }
func (f *GrepOutputFilter) Description() string {
	return "Output filter: searches output with a regular expression, returning matching lines with source line numbers and optional context."
}
func (f *GrepOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"pattern":{"type":"string","description":"Regular expression to search for."},"ignore_case":{"type":"boolean","default":true},"context":{"type":"integer","minimum":0,"default":0},"max_matches":{"type":"integer","minimum":1,"default":200}`)
}
func (f *GrepOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *GrepOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	var params struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if params.Pattern == "" {
		return "", errors.New("pattern is required")
	}
	var options map[string]json.RawMessage
	if err := json.Unmarshal(args, &options); err != nil {
		return "", err
	}
	ignoreCase := true
	if raw, ok := options["ignore_case"]; ok {
		if err := json.Unmarshal(raw, &ignoreCase); err != nil {
			return "", errors.New("ignore_case must be a boolean")
		}
	}
	pattern := params.Pattern
	if ignoreCase {
		pattern = `(?i)` + pattern
	}
	options["pattern"], _ = json.Marshal(pattern)
	searchArgs, err := json.Marshal(options)
	if err != nil {
		return "", err
	}
	return findMatchingLines(text, searchArgs, pattern, false)
}
