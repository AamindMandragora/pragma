package cmd

import (
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/AamindMandragora/pragma/internal/app"
	"github.com/AamindMandragora/pragma/internal/config"
	"github.com/AamindMandragora/pragma/internal/db"
	"github.com/AamindMandragora/pragma/internal/tui"
)

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
