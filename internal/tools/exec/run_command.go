package exec

import (
	"encoding/json"
	"time"

	"github.com/AamindMandragora/pragma/internal/process"
	"github.com/AamindMandragora/pragma/internal/tools"
)

// run command tools must have a process manager
type RunCommandTool struct {
	Manager *process.Manager
}

func (r *RunCommandTool) Name() string {
	return "run_command"
}

func (r *RunCommandTool) Access() tools.AccessLevel {
	return tools.AccessExecute
}

func (r *RunCommandTool) Description() string {
	return "Runs a given command"
}

func (r *RunCommandTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"command": {"type": "string", "description": "Command to be run"}, "timeout": {"type": "integer", "description": "Timeout in seconds before terminating the process, no timeout by default"}}, "required": ["command"]}`)
}

func (r *RunCommandTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		Command string `json:"command"`
	}
	json.Unmarshal(args, &params)
	return params.Command
}

func (r *RunCommandTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if !process.CheckInput(params.Command) {
		return "access denied: command references an ignored file", nil
	}
	// runs the command through the process manager
	proc, err := r.Manager.Start(params.Command, time.Duration(params.Timeout)*time.Second, "SHELL")
	if err != nil {
		return "", err
	}
	// waits for the result
	result := proc.Wait()

	// Tool output is intentionally complete. Use the composable output filters
	// (for example find_issues or tail) when a narrower result is wanted.
	output := result.Format(params.Timeout)

	// closes process result buffers
	result.Stdout.Close()
	result.Stderr.Close()

	return output, nil
}
