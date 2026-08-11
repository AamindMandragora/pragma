package tools

import (
	"encoding/json"
	"errors"
	"fmt"
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

// alias for the level of permissions needed
type AccessLevel int

const (
	AccessRead AccessLevel = iota
	AccessWrite
	AccessExecute
)

// access returns whether a tool needs read, write, or exec permissions
type AccessTool interface {
	Tool
	Access() AccessLevel
}

// alias for strings representing permissions (Read, Write, eXec)
type ConfirmMode string

const (
	ConfirmModeN   ConfirmMode = "---"
	ConfirmModeR   ConfirmMode = "r--"
	ConfirmModeRW  ConfirmMode = "rw-"
	ConfirmModeRWX ConfirmMode = "rwx"
)

// validates permission string
func ParseConfirmMode(s string) (ConfirmMode, error) {
	switch ConfirmMode(s) {
	case ConfirmModeN, ConfirmModeR, ConfirmModeRW, ConfirmModeRWX:
		return ConfirmMode(s), nil
	default:
		return "", fmt.Errorf("invalid confirm mode %q (use ---, r--, rw-, or rwx)", s)
	}
}

// returns whether a tool needs confirmation
func (m ConfirmMode) RequiresConfirm(level AccessLevel) bool {
	switch m {
	case ConfirmModeRWX:
		return false
	case ConfirmModeRW:
		return level >= AccessExecute
	case ConfirmModeR:
		return level >= AccessWrite
	case ConfirmModeN:
		return true
	default:
		return level >= AccessWrite
	}
}

// result of confirm prompt
type ConfirmAction int

const (
	ConfirmApprove ConfirmAction = iota
	ConfirmRejectSilent
	ConfirmRejectReason
)

// struct returned by confirm channel
type ConfirmResponse struct {
	Action ConfirmAction
	Reason string
}

// dispatch returns this on tool call rejection
type Rejection struct {
	Reason string
}

// sanitizes empty rejection
func (r *Rejection) Error() string {
	if r.Reason == "" {
		return SilentRejectMessage
	}
	return r.Reason
}

const SilentRejectMessage = "user rejected this action, try a different approach"

// returns a rejection if err is one
func AsRejection(err error) (*Rejection, bool) {
	var r *Rejection
	if errors.As(err, &r) {
		return r, true
	}
	return nil, false
}

// registry holds the tools and interactive callbacks for the TUI
type Registry struct {
	Tools       map[string]Tool
	Filters     map[string]OutputFilter
	ConfirmMode ConfirmMode
	Confirm     func(toolName string, summary string) ConfirmResponse
	// AskUser pauses execution and returns the user's free-text answer
	AskUser func(tried []string, problem, question string) string
}

// creates a new registry
func NewRegistry() *Registry {
	return &Registry{Tools: make(map[string]Tool), Filters: make(map[string]OutputFilter), ConfirmMode: ConfirmModeR}
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

// normalizes access level to all tools
func toolAccess(tool Tool) AccessLevel {
	if at, ok := tool.(AccessTool); ok {
		return at.Access()
	}
	return AccessExecute
}

// returns the tool's summary and whether it needs confirmation
func toolSummary(tool Tool, args json.RawMessage) (string, bool) {
	if ct, ok := tool.(ConfirmableTool); ok {
		summary := ct.ConfirmSummary(args)
		if summary == "" {
			return "", false
		}
		return summary, true
	}
	if len(args) == 0 || string(args) == "{}" || string(args) == "null" {
		return tool.Name(), true
	}
	return string(args), true
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
	// confirm when the mode requires it for this tool's access level
	if r.Confirm != nil && r.ConfirmMode.RequiresConfirm(toolAccess(tool)) {
		summary, ask := toolSummary(tool, toolArgs)
		if ask {
			resp := r.Confirm(tool.Name(), summary)
			switch resp.Action {
			case ConfirmApprove:
				// continue to execute
			case ConfirmRejectReason:
				return "", &Rejection{Reason: resp.Reason}
			default:
				return "", &Rejection{}
			}
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
