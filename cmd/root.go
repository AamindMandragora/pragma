package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/AamindMandragora/pragma/internal/app"
	"github.com/AamindMandragora/pragma/internal/config"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/tools"
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
	headless     bool    // true when pragma should run one task without the TUI
)

// defines pragma by the text command the user will type in, short and long help descriptions, and a function to run once called
var rootCmd = &cobra.Command{
	Use:   "pragma [prompt]",
	Short: "pragma is the CLI agentic code helper",
	Long:  "pragma launches an interactive TUI by default, or runs one task in headless mode.",
	RunE: func(cmd *cobra.Command, args []string) error {
		if showVersion {
			fmt.Printf("pragma version %s\n", Version)
			return nil
		}
		if headless {
			return runHeadless(args)
		}
		launchTUI()
		return nil
	},
}

var headlessCmd = &cobra.Command{
	Use:   "headless [prompt]",
	Short: "run one task without starting the interactive TUI",
	Args:  cobra.ArbitraryArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runHeadless(args)
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
	rootCmd.Flags().BoolVar(&headless, "headless", false, "run one task without starting the interactive TUI")
	rootCmd.PersistentFlags().StringVarP(&configFile, "config", "c", "", "path to a custom configuration file")
	rootCmd.PersistentFlags().Float64VarP(&budget, "budget", "b", 0, "max dollar budget for this session")
	rootCmd.PersistentFlags().StringVarP(&oldSession, "resume", "r", "", "start an old session from where you left off given the uuid")
	rootCmd.Flags().BoolVarP(&listSessions, "sessions", "s", false, "shows a list of recent session information, max 10 entries")
	rootCmd.AddCommand(headlessCmd)
}

// installConfig copies a config supplied on the command line into the normal
// project-local location used by the rest of Pragma.
func installConfig() error {
	if configFile == "" {
		return nil
	}
	if err := os.MkdirAll(".agent", 0755); err != nil {
		return err
	}
	data, err := os.ReadFile(configFile)
	if err != nil {
		return err
	}
	return os.WriteFile(".agent/config.toml", data, 0644)
}

func loadRuntime() (*app.Runtime, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	if err := db.Connect(); err != nil {
		return nil, err
	}
	if err := db.Migrate(); err != nil {
		return nil, err
	}
	return app.New(cfg)
}

func printSessions() {
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
}

// performs the setup and starts the TUI
func launchTUI() {
	if err := installConfig(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	// if there's no .agent/config.toml, we start the tui with nil agent to trigger onboarding
	if _, err := os.Stat(".agent/config.toml"); os.IsNotExist(err) {
		tui.Start(nil)
		// if onboarding still couldn't create the config.toml, return
		if _, err := os.Stat(".agent/config.toml"); os.IsNotExist(err) {
			return
		}
	}

	runtime, err := loadRuntime()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return
	}
	if listSessions {
		printSessions()
		return
	}

	a, manager := runtime.NewAgent(oldSession, true)
	a.Budget = budget

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	defer manager.Cleanup()

	// if we catch a signal that isn't handled in the TUI, then we cleanup manually and exit
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "\n[pragma] Interrupted, cleaning up...")
		manager.Cleanup()
		os.Exit(0)
	}()

	// starts the TUI with the agent
	tui.Start(a)
}

func HeadlessPrompt(args []string, input io.Reader) (string, error) {
	if len(args) > 0 {
		return strings.TrimSpace(strings.Join(args, " ")), nil
	}
	data, err := io.ReadAll(input)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

// runHeadless executes exactly one agent task and writes only the final agent
// response to stdout. Prompt input can be supplied as arguments or via stdin;
// diagnostics and errors go to stderr so the mode is safe to compose in a
// shell pipeline.
func runHeadless(args []string) error {
	prompt, err := HeadlessPrompt(args, os.Stdin)
	if err != nil {
		return fmt.Errorf("read headless prompt: %w", err)
	}
	if prompt == "" {
		return fmt.Errorf("headless mode requires a prompt argument or stdin input")
	}
	if err := installConfig(); err != nil {
		return err
	}
	runtime, err := loadRuntime()
	if err != nil {
		return err
	}
	if listSessions {
		printSessions()
		return nil
	}

	a, manager := runtime.NewAgent(oldSession, true)
	a.Budget = budget
	// Headless execution cannot pause for a confirmation or an answer. The
	// caller explicitly selected this mode, so confirmable tools proceed and
	// ask_user receives a deterministic response instead of blocking forever.
	a.Registry.Confirm = func(string, string) tools.ConfirmResponse { return tools.ConfirmResponse{Action: tools.ConfirmApprove} }
	a.Registry.AskUser = func([]string, string, string) string {
		return "No interactive user is available in headless mode. Make the safest reasonable assumption or report the blocker."
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer manager.Cleanup()
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	go func() {
		select {
		case <-sigChan:
			cancel()
			manager.Cleanup()
		case <-ctx.Done():
		}
	}()

	result, err := a.Run(ctx, prompt)
	if err != nil {
		return err
	}
	fmt.Fprintln(os.Stdout, result)
	return nil
}
