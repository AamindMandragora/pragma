package filters

import "encoding/json"

type HeadOutputFilter struct{}

func (f *HeadOutputFilter) Name() string { return "head" }
func (f *HeadOutputFilter) Description() string {
	return "Output filter: returns the first n lines (default 20). Use in filters:[{name: head, args: {n: 10}}]."
}
func (f *HeadOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"n":{"type":"integer","minimum":1,"default":20,"description":"Number of lines to keep."}`)
}
func (f *HeadOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *HeadOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	n, err := parsePositiveInt(args, "n", defaultHeadLines)
	if err != nil {
		return "", err
	}
	lines, trailing := outputLines(text)
	if n > len(lines) {
		n = len(lines)
	}
	return joinOutputLines(lines[:n], trailing || n < len(lines)), nil
}

type TailOutputFilter struct{}

func (f *TailOutputFilter) Name() string { return "tail" }
func (f *TailOutputFilter) Description() string {
	return "Output filter: returns the last n lines (default 20). Use in filters:[{name: tail, args: {n: 50}}]."
}
func (f *TailOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"n":{"type":"integer","minimum":1,"default":20,"description":"Number of lines to keep."}`)
}
func (f *TailOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *TailOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	n, err := parsePositiveInt(args, "n", defaultTailLines)
	if err != nil {
		return "", err
	}
	lines, trailing := outputLines(text)
	if n > len(lines) {
		n = len(lines)
	}
	return joinOutputLines(lines[len(lines)-n:], trailing), nil
}
