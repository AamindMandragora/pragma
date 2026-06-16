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
	filetools "github.com/AamindMandragora/pragma/internal/tools/files"
	gittools "github.com/AamindMandragora/pragma/internal/tools/git"
	"github.com/AamindMandragora/pragma/internal/tui"
	"github.com/spf13/cobra" // the package that allows us to create pragma as a CLI tool, used because it's industry standard
)

const Version = "1.0.0"

var (
	configFile   string  // path to a custom configuration file
	showVersion  bool    // true if the user wants to see the version
	budget       float64 // initial budget pragma will work under
	oldSession   string  // uuid of a session to be resumed
	listSessions bool    // true if the user wants to list the past sessions
)

// defines pragma by the text command the user will type in, short and long help descriptions, and a function to run once called
var rootCmd = &cobra.Command{
	Use:   "pragma",
	Short: "pragma is the CLI agentic code helper",
	Long:  `pragma launches an interactive TUI by default, or runs specialized subcommands.`,
	Run: func(cmd *cobra.Command, args []string) {
		// if the user wants to see the version, print it
		if showVersion {
			fmt.Printf("pragma version %s\n", Version)
			return
		}
		// otherwise, launch the terminal UI
		launchTUI()
	},
}

// runs the specified function in pragma's rootCmd, prints error if any happened
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// all init functions in a project get run before the main function in main.go, this one adds the version, config, and budget flags to the cli tool
func init() {
	rootCmd.Flags().BoolVarP(&showVersion, "version", "v", false, "print version information")
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "path to custom configuration file")
	rootCmd.PersistentFlags().Float64VarP(&budget, "budget", "b", 0, "max dollar budget for this session")
	rootCmd.PersistentFlags().StringVarP(&oldSession, "resume", "r", "", "start an old session from where you left off given the uuid")
	rootCmd.Flags().BoolVarP(&listSessions, "sessions", "s", false, "shows a list of recent session information, max 10 entries")
}

// performs the setup and starts the TUI
func launchTUI() {
	// if the user gave us a config file, we attempt to read it and copy it to .agent/config.toml
	if configFile != "" {
		os.MkdirAll(".agent", 0755)
		data, err := os.ReadFile(configFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return
		}
		os.WriteFile(".agent/config.toml", data, 0644)
	}
	// if there's no .agent/config.toml, we start the tui with nil agent to trigger onboarding
	if _, err := os.Stat(".agent/config.toml"); os.IsNotExist(err) {
		tui.Start(nil)
		// if onboarding still couldn't create the config.toml, return
		if _, err := os.Stat(".agent/config.toml"); os.IsNotExist(err) {
			return
		}
	}
	// loads config from .agent/config.toml into a struct
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	// connects to the database
	err = db.Connect()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	// makes sure the database structure is what we expect
	err = db.Migrate()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}

	// if the user wants to see the sessions print them out now that we've connected to the db
	if listSessions {
		sessions, err := db.ListSessions(10)
		if err != nil {
			fmt.Print("couldn't fetch sessions\n")
		} else if sessions == nil {
			fmt.Print("no previous sessions\n")
		} else {
			fmt.Print("sessions:\n")
			for _, session := range sessions {
				fmt.Printf("\t- %s\t%s\n", session.Id.String(), session.Title)
			}
		}
		return
	}
	// sets up the list holding every (model, minPercentBudgetSpent) pair
	var tiers []llm.ModelTier
	// loops through every entry in the config
	for _, t := range cfg.Model.Tiers {
		// if there's no api key env var name specified, then throw an error
		if t.ApiKeyVarName == "" {
			fmt.Fprintf(os.Stderr, "API key not set for tier %s: export %s=<key>\n", t.Model, t.ApiKeyVarName)
			return
		}
		// creates the provider object
		p := llm.MakeProvider(t.ProviderName, t.ApiKeyVarName)
		// if the model has a special maxTokens, use that, otherwise default to 4096
		maxTokens := t.MaxTokens
		if maxTokens == 0 {
			maxTokens = 4096
		}
		// creates the model object and adds it to the list
		model := &llm.Model{
			Name:        t.Model,
			MaxTokens:   maxTokens,
			Temperature: t.Temperature,
			Provider:    p,
			ToolMode:    llm.ToolModeForProvider(t.ProviderName),
		}
		tiers = append(tiers, llm.ModelTier{Model: model, Threshold: t.Threshold})
	}

	// initializes the process manager and the tools registry
	manager := process.NewManager()
	registry := tools.NewRegistry()
	// registers all the tools we have
	for _, tool := range filetools.RegisterAll() {
		registry.Register(tool)
	}
	registry.Register(&tools.WebFetchTool{})
	// passes the manager to the run command tool as well as a 5 min default timeout
	registry.Register(&tools.RunCommandTool{Manager: manager, Timeout: 5 * time.Minute})
	for _, tool := range gittools.RegisterAll() {
		registry.Register(tool)
	}
	tools.LoadPlugins(registry, ".agent/tools", manager)

	// creates an agent that holds the model tiers and the registry (resumes an old session if one was given)
	var a *agent.Agent
	if oldSession == "" {
		a = agent.NewAgent(tiers, registry)
	} else {
		a = agent.ResumeAgent(oldSession, tiers, registry)
	}
	// sets the agent's budget
	a.Budget = budget
	// starts the TUI with the agent
	tui.Start(a)
}
