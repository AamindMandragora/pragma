package exec

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/process"
	"github.com/AamindMandragora/pragma/internal/tools"
)

type RunPythonTool struct {
	Manager *process.Manager
}

func (r *RunPythonTool) Name() string {
	return "run_python"
}

func (r *RunPythonTool) Access() tools.AccessLevel {
	return tools.AccessExecute
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

	// Tool output is intentionally complete. Compose an explicit output filter
	// such as find_issues or tail when a narrower result is wanted.
	output := result.Format(params.Timeout)
	result.Stdout.Close()
	result.Stderr.Close()
	return output, nil
}
