package tools

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/AamindMandragora/pragma/internal/process"
)

// all tool plugins must have a name, description, schema, command, and a boolean determining whether or not it requires confirmation
type PluginManifest struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Command     string          `json:"command"`
	Confirm     bool            `json:"confirm"`
}

// plugin tools consist of the above manifest and the process manager
type PluginTool struct {
	Manifest PluginManifest
	Manager  *process.Manager
}

// reads */tool.json files from the given path, creates the manifest by unmarshaling the data, then gives it the manager and registers it
func LoadPlugins(registry *Registry, path string, manager *process.Manager) {
	entries, err := os.ReadDir(path)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.IsDir() {
			manifestPath := filepath.Join(path, entry.Name(), "tool.json")
			data, err := os.ReadFile(manifestPath)
			if err != nil {
				continue
			}
			var manifest PluginManifest
			if err := json.Unmarshal(data, &manifest); err != nil {
				continue
			}
			ptool := &PluginTool{Manifest: manifest, Manager: manager}
			registry.Register(ptool)
		}
	}
}

func (p *PluginTool) Name() string {
	return p.Manifest.Name
}

func (p *PluginTool) Description() string {
	return p.Manifest.Description
}

func (p *PluginTool) Schema() json.RawMessage {
	return p.Manifest.Schema
}

func (p *PluginTool) Execute(args json.RawMessage) (string, error) {
	var params map[string]interface{}
	if err := json.Unmarshal(args, &params); err != nil {
		return "", err
	}
	cmd := p.Manifest.Command
	// replaces the key placeholders in the command with the values
	for key, val := range params {
		placeholder := "{{" + key + "}}"
		cmd = strings.ReplaceAll(cmd, placeholder, fmt.Sprintf("%v", val))
	}
	// runs the command through the process manager
	proc, err := p.Manager.Start(cmd, 5*time.Minute)
	if err != nil {
		return "", err
	}
	// waits on the result
	result := proc.Wait()
	output := result.Stdout.String()
	stderr := result.Stderr.String()
	// adds stderr to the output
	if stderr != "" {
		output += "\nstderr:\n" + stderr
	}
	// adds exit code to the output
	if result.ExitCode != 0 {
		output += fmt.Sprintf("\nexit code: %d", result.ExitCode)
	}
	// closes buffers and returns
	result.Stdout.Close()
	result.Stderr.Close()
	return output, nil
}

// plugin confirm summaries are just the key/value arg pairs
func (p *PluginTool) ConfirmSummary(args json.RawMessage) string {
	if !p.Manifest.Confirm {
		return ""
	}
	var params map[string]interface{}
	json.Unmarshal(args, &params)
	cmd := p.Manifest.Command
	for key, val := range params {
		cmd = strings.ReplaceAll(cmd, "{{"+key+"}}", fmt.Sprintf("%v", val))
	}
	return cmd
}
