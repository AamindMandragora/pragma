// Package subagent provides the tool definition used to delegate work to a
// non-interactive Pragma agent.
package subagent

import (
	"encoding/json"
	"errors"
	"strings"
)

// Runner executes one headless agent task. The budget is zero when the caller
// did not provide a per-task budget.
type Runner func(prompt string, budget float64) (string, error)

// Tool delegates a task to a separate headless agent session.
type Tool struct {
	Run Runner
}

func (t *Tool) Name() string {
	return "subagent"
}

func (t *Tool) Description() string {
	return "Delegate a self-contained coding task to a separate headless Pragma agent. The subagent has the repository tools but cannot ask interactive questions or delegate again."
}

func (t *Tool) Schema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"prompt": {
				"type": "string",
				"description": "A self-contained task for the subagent, including relevant context and the expected result"
			},
			"budget": {
				"type": "number",
				"minimum": 0,
				"description": "Optional maximum dollar budget for this subagent task"
			}
		},
		"required": ["prompt"]
	}`)
}

func (t *Tool) Execute(args json.RawMessage) (string, error) {
	if t.Run == nil {
		return "", errors.New("headless subagent runner is not configured")
	}

	var params struct {
		Prompt string  `json:"prompt"`
		Budget float64 `json:"budget"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	params.Prompt = strings.TrimSpace(params.Prompt)
	if params.Prompt == "" {
		return "", errors.New("prompt is required")
	}
	if params.Budget < 0 {
		return "", errors.New("budget must be non-negative")
	}

	return t.Run(params.Prompt, params.Budget)
}
