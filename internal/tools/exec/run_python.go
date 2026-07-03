package exec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/process"
)

type RunPythonTool struct {
	Manager *process.Manager
}

func (r *RunPythonTool) Name() string {
	return "run_python"
}

func (r *RunPythonTool) Description() string {
	return "Runs Python code"
}

func (r *RunPythonTool) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"code":{"type":"string","description":"Python code to run"},"timeout":{"type":"integer","description":"Timeout in seconds before terminating the process, no timeout by default"}},"required":["code"]}`)
}

func (r *RunPythonTool) ConfirmSummary(args json.RawMessage) string {
	var params struct {
		Code string `json:"code"`
	}
	json.Unmarshal(args, &params)
	code := strings.TrimSpace(params.Code)
	if len(code) > 120 {
		return code[:120] + "..."
	}
	return code
}

func (r *RunPythonTool) Execute(args json.RawMessage) (string, error) {
	var params struct {
		Code    string `json:"code"`
		Timeout int    `json:"timeout,omitempty"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	if !process.CheckInput(params.Code) {
		return "access denied: code references an ignored file", nil
	}
	code := strings.ReplaceAll(params.Code, "\r\n", "\n")
	tmpDir := os.TempDir()
	if tmpDir == "" {
		tmpDir = "."
	}
	path := filepath.Join(tmpDir, fmt.Sprintf("pragma-run-python-%d.py", time.Now().UnixNano()))
	if err := os.WriteFile(path, []byte(code), 0600); err != nil {
		return "", err
	}
	defer os.Remove(path)
	proc, err := r.Manager.Start(path, time.Duration(params.Timeout)*time.Second, "SHELL")
	if err != nil {
		return "", err
	}
	result := proc.Wait()

	var output string
	if result.Stdout.Lines() > 100 {
		errors := result.Stdout.Filter("error|warning|fatal|FAIL|Traceback")
		if len(errors) > 0 {
			output = fmt.Sprintf("(%d lines, showing %d errors/warnings)\n\n%s", result.Stdout.Lines(), len(errors), strings.Join(errors, "\n"))
		} else {
			tail := result.Stdout.Tail(50)
			output = fmt.Sprintf("(%d lines, showing last 50)\n\n%s", result.Stdout.Lines(), strings.Join(tail, "\n"))
		}
	} else {
		output = result.Stdout.String()
	}

	stderr := result.Stderr.String()
	if stderr != "" {
		output += "\nstderr:\n" + stderr
	}
	if result.Status == "timeout" {
		output += fmt.Sprintf("\n\nProcess timed out after %d seconds", params.Timeout)
	}
	if result.ExitCode != 0 {
		output += fmt.Sprintf("\nExit code: %d", result.ExitCode)
	}
	result.Stdout.Close()
	result.Stderr.Close()
	return output, nil
}
