package git

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"

	"github.com/AamindMandragora/pragma/internal/tools"
)

func RegisterAll() []tools.Tool {
	return []tools.Tool{
		NewStatusTool(),
		NewDiffTool(),
		NewLogTool(),
		NewCommitTool(),
		NewBranchTool(),
		NewStashTool(),
	}
}

type gitTool struct {
	name, description string
	schema            json.RawMessage
}

func (b *gitTool) Name() string            { return b.name }
func (b *gitTool) Description() string     { return b.description }
func (b *gitTool) Schema() json.RawMessage { return b.schema }

func runGit(args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	output := strings.TrimSpace(out.String())
	if s := strings.TrimSpace(errOut.String()); s != "" {
		if output != "" {
			output += "\n"
		}
		output += s
	}
	if err != nil && output == "" {
		output = err.Error()
	}
	return output, err
}

func truncate(output string, err error) (string, error) {
	if err != nil {
		return output, err
	}
	if len(output) > 12000 {
		return output[:12000] + "\n...truncated...", nil
	}
	return output, nil
}

func required(name, value string) error {
	if value == "" {
		return fmt.Errorf("%s is required", name)
	}
	return nil
}
