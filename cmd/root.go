package cmd

import (
	"fmt"
	"os"

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
