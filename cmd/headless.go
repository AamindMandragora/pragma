package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/AamindMandragora/pragma/internal/tools"
)

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
