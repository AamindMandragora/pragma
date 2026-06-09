package tools

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/process"
)

// run command tools must have a process manager and default timeout
type RunCommandTool struct {
	Manager *process.Manager
	Timeout time.Duration
}

func (r *RunCommandTool) Name() string {
	return "run_command"
}

func (r *RunCommandTool) Description() string {
	return "Runs a given command"
}

func (r *RunCommandTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type": "object", "properties": {"command": {"type": "string", "description": "Command to be run"}}, "required": ["command"]}`)
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
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	// runs the command through the process manager
	proc, err := r.Manager.Start(params.Command, r.Timeout)
	if err != nil {
		return "", err
	}
	// waits for the result
	result := proc.Wait()

	var output string
	// if the result was long then we filter it for important error info
	if result.Stdout.Lines() > 100 {
		errors := result.Stdout.Filter("error|warning|fatal|error|FAIL")
		// if there are errors print those otherwise return last 50 lines
		if len(errors) > 0 {
			output = fmt.Sprintf("(%d lines, showing %d errors/warnings)\n\n%s", result.Stdout.Lines(), len(errors), strings.Join(errors, "\n"))
		} else {
			tail := result.Stdout.Tail(50)
			output = fmt.Sprintf("(%d lines, showing last 50)\n\n%s", result.Stdout.Lines(), strings.Join(tail, "\n"))
		}
	} else {
		output = result.Stdout.String()
	}

	// appends the stderr to the output
	stderr := result.Stderr.String()
	if stderr != "" {
		output += "\nstderr:\n" + stderr
	}

	if result.Status == "timeout" {
		output += fmt.Sprintf("\n\nProcess timed out after %s", r.Timeout)
	}

	if result.ExitCode != 0 {
		output += fmt.Sprintf("\nExit code: %d", result.ExitCode)
	}

	// closes process result buffers
	result.Stdout.Close()
	result.Stderr.Close()

	return output, nil
}
