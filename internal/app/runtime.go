// Package app contains the shared Pragma runtime used by both the TUI and
// headless entry points.
package app

import (
	"context"
	"fmt"

	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/config"
	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/process"
	"github.com/AamindMandragora/pragma/internal/tools"
	exectools "github.com/AamindMandragora/pragma/internal/tools/exec"
	filetools "github.com/AamindMandragora/pragma/internal/tools/files"
	"github.com/AamindMandragora/pragma/internal/tools/filters"
	gittools "github.com/AamindMandragora/pragma/internal/tools/git"
	"github.com/AamindMandragora/pragma/internal/tools/interaction"
	"github.com/AamindMandragora/pragma/internal/tools/plugins"
	"github.com/AamindMandragora/pragma/internal/tools/subagent"
	"github.com/AamindMandragora/pragma/internal/tools/web"
)

// Runtime contains the configured models used to create independent agent
// sessions. Sessions receive their own registry and process manager.
type Runtime struct {
	Config *config.Config
	Tiers  []llm.ModelTier
}

// New validates the model configuration and resolves its providers.
func New(cfg *config.Config) (*Runtime, error) {
	if cfg == nil {
		return nil, fmt.Errorf("configuration is nil")
	}
	if len(cfg.Model.Tiers) == 0 {
		return nil, fmt.Errorf("configuration must contain at least one model tier")
	}

	r := &Runtime{Config: cfg}
	for _, tier := range cfg.Model.Tiers {
		if tier.ApiKeyVarName == "" {
			return nil, fmt.Errorf("API key not set for tier %s: configure api_key_var_name", tier.Model)
		}
		maxTokens := tier.MaxTokens
		if maxTokens == 0 {
			maxTokens = cfg.Behavior.MaxOutputTokens
		}
		if maxTokens == 0 {
			maxTokens = 8192
		}
		r.Tiers = append(r.Tiers, llm.ModelTier{
			Model: &llm.Model{
				Name:        tier.Model,
				MaxTokens:   maxTokens,
				Temperature: tier.Temperature,
				Effort:      tier.Effort,
				Provider:    llm.MakeProvider(tier.ProviderName, tier.ApiKeyVarName),
				ToolMode:    llm.ToolModeForProvider(tier.ProviderName),
			},
			Threshold: tier.Threshold,
		})
	}
	return r, nil
}

// NewAgent creates a fresh or resumed agent. The top-level agent gets the
// subagent tool; child agents do not, which keeps delegation bounded.
func (r *Runtime) NewAgent(sessionID string, enableSubagent bool) (*agent.Agent, *process.Manager) {
	manager := process.NewManager()
	registry := tools.NewRegistry()
	for _, tool := range filetools.RegisterAll() {
		registry.Register(tool)
	}
	registry.Register(&web.WebFetchTool{})
	registry.Register(&interaction.AskUserTool{})
	filters.RegisterBuiltinOutputFilters(registry, r.Tiers[0].Model)
	registry.Register(&exectools.RunCommandTool{Manager: manager})
	registry.Register(&exectools.RunPythonTool{Manager: manager})
	for _, tool := range gittools.RegisterAll() {
		registry.Register(tool)
	}
	plugins.LoadPlugins(registry, ".agent/tools", manager)

	if enableSubagent {
		registry.Register(&subagent.Tool{Run: func(prompt string, budget float64) (string, error) {
			child, childManager := r.NewAgent("", false)
			defer childManager.Cleanup()
			child.Budget = budget
			// There is no terminal available to a child. Confirmable tools are
			// allowed because delegation itself is the parent's explicit choice.
			child.Registry.Confirm = func(string, string) tools.ConfirmResponse { return tools.ConfirmResponse{Action: tools.ConfirmApprove} }
			child.Registry.AskUser = func([]string, string, string) string {
				return "No interactive user is available in headless subagent mode. Make the safest reasonable assumption or report the blocker."
			}
			return child.Run(context.Background(), prompt)
		}})
	}

	var a *agent.Agent
	if sessionID == "" {
		a = agent.NewAgent(r.Tiers, registry, manager)
	} else {
		a = agent.ResumeAgent(sessionID, r.Tiers, registry, manager)
	}
	a.ContextMaxTokens = r.Config.Context.MaxInputTokens
	return a, manager
}
