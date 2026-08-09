package tools

import (
	"encoding/json"
	"errors"
	"fmt"

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

// registry holds the tools and a confirm function
type Registry struct {
	Tools       map[string]Tool
	ConfirmMode ConfirmMode
	Confirm     func(toolName string, summary string) ConfirmResponse
}

// creates a new registry
func NewRegistry() *Registry {
	return &Registry{Tools: make(map[string]Tool), ConfirmMode: ConfirmModeR}
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
	// attempts to find tool in map
	tool, ok := r.Tools[name]
	if !ok {
		return "", errors.New("Tool not found")
	}
	// confirm when the mode requires it for this tool's access level
	if r.Confirm != nil && r.ConfirmMode.RequiresConfirm(toolAccess(tool)) {
		summary, ask := toolSummary(tool, args)
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
	return tool.Execute(args)
}
