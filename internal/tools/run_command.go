package tools

import (
	"encoding/json"
	"os/exec"
)

type RunCommandTool struct {
	Confirm func(command string) bool
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

func (r *RunCommandTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Command string `json:"command"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if r.Confirm != nil && !r.Confirm(params.Command) {
		return "Command rejected by user", nil
	}
	var cmd = exec.Command("sh", "-c", params.Command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output) + "\n" + err.Error(), nil
	}
	return string(output), nil
}
