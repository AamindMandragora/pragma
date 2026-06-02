package tools

import (
	"encoding/json"
	"errors"

	"github.com/AamindMandragora/pragma/internal/llm"
)

type Tool interface {
	Name() string
	Description() string
	Schema() json.RawMessage
	Execute(args json.RawMessage) (string, error)
}

type Registry struct {
	Tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{Tools: make(map[string]Tool)}
}

func (r *Registry) Register(tool Tool) {
	r.Tools[tool.Name()] = tool
}

func (r *Registry) List() []llm.ToolDef {
	var tools []llm.ToolDef
	for _, tool := range r.Tools {
		tools = append(tools, llm.ToolDef{Name: tool.Name(), Description: tool.Description(), InputSchema: tool.Schema()})
	}
	return tools
}

func (r *Registry) Dispatch(name string, args json.RawMessage) (string, error) {
	tool, ok := r.Tools[name]
	if !ok {
		return "", errors.New("Tool not found")
	}
	return tool.Execute(args)
}