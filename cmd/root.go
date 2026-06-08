package cmd

import (
	"fmt"
	"os"
	"time"

	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/config"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/process"
	"github.com/AamindMandragora/pragma/internal/tools"
	"github.com/AamindMandragora/pragma/internal/tui"
	"github.com/spf13/cobra"
)

const Version = "1.0.0"

var (
	configFile  string
	showVersion bool
	budget      float64
)

var rootCmd = &cobra.Command{
	Use:   "pragma",
	Short: "pragma is the CLI agentic code helper",
	Long:  `pragma launches an interactive TUI by default, or runs specialized subcommands.`,
	Run: func(cmd *cobra.Command, args []string) {
		if showVersion {
			fmt.Printf("pragma version %s\n", Version)
			return
		}
		launchTUI()
	},
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "print version information")
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "path to custom configuration file")
	rootCmd.PersistentFlags().Float64VarP(&budget, "budget", "b", 0, "max dollar budget for this session")
}

func launchTUI() {
	if configFile != "" {
		os.MkdirAll(".agent", 0755)
		data, err := os.ReadFile(configFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		os.WriteFile(".agent/config.toml", data, 0644)
	}
	if _, err := os.Stat(".agent/config.toml"); os.IsNotExist(err) {
		tui.Start(nil, nil)
		if _, err := os.Stat(".agent/config.toml"); os.IsNotExist(err) {
			return
		}
	}
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	err = db.Connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	err = db.Migrate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	var tiers []llm.ModelTier
	for _, t := range cfg.Model.Tiers {
		p := llm.MakeProvider(t.ProviderName, t.ApiKeyVarName)
		if t.ApiKeyVarName == "" {
			fmt.Fprintf(os.Stderr, "API key not set for tier %s: export %s=<key>\n", t.Model, t.ApiKeyVarName)
			return
		}
		maxTokens := t.MaxTokens
		if maxTokens == 0 {
			maxTokens = 4096
		}
		model := &llm.Model{
			Name:        t.Model,
			MaxTokens:   maxTokens,
			Temperature: t.Temperature,
			Provider:    p,
			ToolMode:    llm.ToolModeForProvider(t.ProviderName),
		}
		tiers = append(tiers, llm.ModelTier{Model: model, Threshold: t.Threshold})
	}

	manager := process.NewManager()
	registry := tools.NewRegistry()
	registry.Register(&tools.ReadFileTool{})
	registry.Register(&tools.WriteFileTool{})
	registry.Register(&tools.EditFileTool{})
	registry.Register(&tools.RunCommandTool{Manager: manager, Timeout: 5 * time.Minute})
	registry.Register(&tools.WebFetchTool{})
	tools.LoadPlugins(registry, ".agent/tools", manager)

	a := agent.NewAgent(tiers[0].Model, registry)
	a.Budget = budget
	a.ModelTiers = tiers
	tui.Start(a, nil)
}
