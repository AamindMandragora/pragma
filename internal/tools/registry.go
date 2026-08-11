package tools

import (
	"encoding/json"
	"errors"
	"sort"

	"github.com/AamindMandragora/pragma/internal/llm"
)

// all tools must have a name, desc, input schema, and a function to run on execution
type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(args json.RawMessage) (string, error)
}

// tools that require confirmation must have a summary to show the user when asking
type ConfirmableTool interface {
	Tool
	ConfirmSummary(args json.RawMessage) string
}

// OutputFilter transforms a tool's complete output. Filters can be called as
// ordinary tools by supplying text, or composed onto another tool with its
// "filters" argument.
type OutputFilter interface {
	Tool
	Apply(text string, args json.RawMessage) (string, error)
}

// registry holds the tools and interactive callbacks for the TUI
type Registry struct {
	Tools   map[string]Tool
	Filters map[string]OutputFilter
	Confirm func(toolName string, summary string) bool
	// AskUser pauses execution and returns the user's free-text answer
	AskUser func(tried []string, problem, question string) string
}

// creates a new registry
func NewRegistry() *Registry {
	return &Registry{Tools: make(map[string]Tool), Filters: make(map[string]OutputFilter)}
}

// registers a tool by adding to the map
func (r *Registry) Register(tool Tool) {
	r.Tools[tool.Name()] = tool
}

// RegisterFilter makes a filter available both as a standalone tool and in a
// tool output pipeline.
func (r *Registry) RegisterFilter(filter OutputFilter) {
	r.Register(filter)
	r.Filters[filter.Name()] = filter
}

// creates tool defs for every tool in the registry and returns list
func (r *Registry) List() []llm.ToolDef {
	var tools []llm.ToolDef
	names := make([]string, 0, len(r.Tools))
	for name := range r.Tools {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		tool := r.Tools[name]
		tools = append(tools, llm.ToolDef{Name: tool.Name(), Description: tool.Description(), InputSchema: r.schemaWithFilters(tool.Schema())})
	}
	return tools
}

// tries to run the named tool with given args, return output
func (r *Registry) Dispatch(name string, args json.RawMessage) (string, error) {
	return r.dispatch(name, args, true)
}

func (r *Registry) dispatch(name string, args json.RawMessage, applyFilters bool) (string, error) {
	// attempts to find tool in map
	tool, ok := r.Tools[name]
	if !ok {
		return "", errors.New("tool not found")
	}
	filterCalls, err := r.parseFilterCalls(args)
	if err != nil {
		return "", err
	}
	toolArgs := stripFilters(args)
	// ask_user blocks on the TUI callback rather than Execute
	if name == "ask_user" {
		var params struct {
			Tried    []string `json:"tried"`
			Problem  string   `json:"problem"`
			Question string   `json:"question"`
		}
		if err := json.Unmarshal(toolArgs, &params); err != nil {
			return "", err
		}
		if r.AskUser == nil {
			return "ask_user is not available in this context", nil
		}
		return r.AskUser(params.Tried, params.Problem, params.Question), nil
	}
	// checks if it needs confirmation
	if ct, ok := tool.(ConfirmableTool); ok {
		// creates the summary and sends confirm request
		summary := ct.ConfirmSummary(toolArgs)
		if summary != "" && r.Confirm != nil && !r.Confirm(ct.Name(), summary) {
			return "Rejected by user", nil
		}
	}
	// executes the tool and returns output
	output, err := tool.Execute(toolArgs)
	if err != nil || !applyFilters || len(filterCalls) == 0 {
		return output, err
	}
	return r.applyFilters(output, filterCalls)
}

type filterCall struct {
	Name string
	Args json.RawMessage
}

func (r *Registry) parseFilterCalls(args json.RawMessage) ([]filterCall, error) {
	if len(args) == 0 || string(args) == "null" {
		return nil, nil
	}
	var envelope map[string]json.RawMessage
	if err := json.Unmarshal(args, &envelope); err != nil {
		return nil, err
	}
	raw, ok := envelope["filters"]
	if !ok || string(raw) == "null" || len(raw) == 0 {
		return nil, nil
	}
	var calls []struct {
		Name string          `json:"name"`
		Args json.RawMessage `json:"args,omitempty"`
	}
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, errors.New("filters must be an array of {name, args} objects")
	}
	result := make([]filterCall, 0, len(calls))
	for _, call := range calls {
		if call.Name == "" {
			return nil, errors.New("each output filter requires a name")
		}
		if _, ok := r.Filters[call.Name]; !ok {
			return nil, errors.New("unknown output filter: " + call.Name)
		}
		filterArgs := call.Args
		if len(filterArgs) == 0 || string(filterArgs) == "null" {
			filterArgs = json.RawMessage(`{}`)
		}
		result = append(result, filterCall{Name: call.Name, Args: filterArgs})
	}
	return result, nil
}

func stripFilters(args json.RawMessage) json.RawMessage {
	if len(args) == 0 || string(args) == "null" {
		return args
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(args, &object); err != nil {
		return args
	}
	if _, ok := object["filters"]; !ok {
		return args
	}
	delete(object, "filters")
	clean, err := json.Marshal(object)
	if err != nil {
		return args
	}
	return clean
}

func (r *Registry) applyFilters(output string, calls []filterCall) (string, error) {
	for _, call := range calls {
		filter := r.Filters[call.Name]
		var err error
		output, err = filter.Apply(output, call.Args)
		if err != nil {
			return "", errors.New("output filter " + call.Name + ": " + err.Error())
		}
	}
	return output, nil
}

func (r *Registry) schemaWithFilters(schema json.RawMessage) json.RawMessage {
	var object map[string]interface{}
	if err := json.Unmarshal(schema, &object); err != nil {
		return schema
	}
	properties, ok := object["properties"].(map[string]interface{})
	if !ok {
		properties = make(map[string]interface{})
		object["properties"] = properties
	}
	names := make([]string, 0, len(r.Filters))
	for name := range r.Filters {
		names = append(names, name)
	}
	sort.Strings(names)
	properties["filters"] = map[string]interface{}{
		"type":        "array",
		"description": "Optional ordered output filters. Omit this field to receive the complete raw tool output.",
		"items": map[string]interface{}{
			"type":     "object",
			"required": []string{"name"},
			"properties": map[string]interface{}{
				"name": map[string]interface{}{
					"type": "string",
					"enum": names,
				},
				"args": map[string]interface{}{
					"type":        "object",
					"description": "Arguments for the selected filter.",
				},
			},
		},
	}
	result, err := json.Marshal(object)
	if err != nil {
		return schema
	}
	return result
}
