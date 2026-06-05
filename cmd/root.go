package cmd

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"strings"

	"github.com/AamindMandragora/pragma/internal/agent"
	"github.com/AamindMandragora/pragma/internal/config"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/llm"
	"github.com/AamindMandragora/pragma/internal/tools"
	"github.com/AamindMandragora/pragma/internal/tui"
	"github.com/spf13/cobra"
)

const Version = "1.0.0"

var (
	configFile  string
	showVersion bool
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
	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "Print version information")
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "Path to custom configuration file")
}

func launchTUI() {
	fmt.Println("Starting pragma TUI...")
	if configFile != "" {
		fmt.Printf("Loading config from: %s\n", configFile)
	}
	cfg, err := config.Load(configFile)
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
	data, err := os.ReadFile(".env")
	var key string = ""

	if err == nil {
		scanner := bufio.NewScanner(bytes.NewReader(data))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			if strings.HasPrefix(line, cfg.Model.ApiKeyVarName+"=") {
				parts := strings.SplitN(line, "=", 2)
				key = strings.Trim(parts[1], `"' `)
				break
			}
		}
	}
	if key == "" {
		key = os.Getenv(cfg.Model.ApiKeyVarName)
	}
	if key == "" {
		fmt.Fprintf(os.Stderr, "Api key not set: export %s=<your key>\n", cfg.Model.ApiKeyVarName)
		return
	}
	var baseURL string
	switch cfg.Model.Provider {
	case "openrouter":
		baseURL = "https://openrouter.ai/api/v1"
	case "openai":
		baseURL = "https://api.openai.com/v1"
	default:
		baseURL = "https://openrouter.ai/api/v1"
	}
	toolMode := cfg.Model.ToolMode
	if toolMode == "" || toolMode == "auto" {
		switch cfg.Model.Provider {
		case "openrouter":
			toolMode = "text"
		case "openai":
			toolMode = "native"
		default:
			toolMode = "text"
		}
	}
	provider := llm.OpenRouterProvider{BaseURL: baseURL, APIKey: key}
	providerConfig := llm.ProviderConfig{ModelName: cfg.Model.ModelName, MaxTokens: cfg.Behavior.MaxOutputTokens, Temperature: 0.0}

	registry := tools.NewRegistry()
	registry.Register(&tools.ReadFileTool{})
	registry.Register(&tools.WriteFileTool{})
	registry.Register(&tools.EditFileTool{})
	registry.Register(&tools.RunCommandTool{})
	registry.Register(&tools.WebFetchTool{})

	a := agent.NewAgent(provider, providerConfig, registry, toolMode)
	tui.Start(a, nil)
}
