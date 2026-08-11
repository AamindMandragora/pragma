package filters

import "encoding/json"

type DedupeOutputFilter struct{}

func (f *DedupeOutputFilter) Name() string { return "dedupe_output" }
func (f *DedupeOutputFilter) Description() string {
	return "Output filter: collapses consecutive duplicate lines, useful for noisy logs and repeated progress output."
}
func (f *DedupeOutputFilter) Schema() json.RawMessage {
	return textFilterSchema("", `,"max_repeats":{"type":"integer","minimum":1,"default":1,"description":"Maximum consecutive copies to keep."}`)
}
func (f *DedupeOutputFilter) Execute(args json.RawMessage) (string, error) {
	text, err := directText(args)
	if err != nil {
		return "", err
	}
	return f.Apply(text, args)
}
func (f *DedupeOutputFilter) Apply(text string, args json.RawMessage) (string, error) {
	maxRepeats, err := parsePositiveInt(args, "max_repeats", defaultDedupeRepeats)
	if err != nil {
		return "", err
	}
	lines, trailing := outputLines(text)
	if len(lines) == 0 {
		return "", nil
	}
	result := make([]string, 0, len(lines))
	previous := ""
	repeats := 0
	for _, line := range lines {
		if line == previous {
			repeats++
		} else {
			previous = line
			repeats = 1
		}
		if repeats <= maxRepeats {
			result = append(result, line)
		}
	}
	return joinOutputLines(result, trailing), nil
}
