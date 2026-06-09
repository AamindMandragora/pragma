package tools

import (
	"encoding/json"
	"errors"

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

// registry holds the tools and a confirm function
type Registry struct {
	Tools   map[string]Tool
	Confirm func(toolName string, summary string) bool
}

// creates a new registry
func NewRegistry() *Registry {
	return &Registry{Tools: make(map[string]Tool)}
}

// registers a tool by adding to the map
func (r *Registry) Register(tool Tool) {
	r.Tools[tool.Name()] = tool
}

// creates tool defs for every tool in the registry and returns list
func (r *Registry) List() []llm.ToolDef {
	var tools []llm.ToolDef
	for _, tool := range r.Tools {
		tools = append(tools, llm.ToolDef{Name: tool.Name(), Description: tool.Description(), InputSchema: tool.Schema()})
	}
	return tools
}

// tries to run the named tool with given args, return output
func (r *Registry) Dispatch(name string, args json.RawMessage) (string, error) {
	// attempts to find tool in map
	tool, ok := r.Tools[name]
	if !ok {
		return "", errors.New("Tool not found")
	}
	// checks if it needs confirmation
	if ct, ok := tool.(ConfirmableTool); ok {
		// creates the summary and sends confirm request
		summary := ct.ConfirmSummary(args)
		if summary != "" && !r.Confirm(ct.Name(), summary) {
			return "Rejected by user", nil
		}
	}
	// executes the tool and returns output
	return tool.Execute(args)
}
