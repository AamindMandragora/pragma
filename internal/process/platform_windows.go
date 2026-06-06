//go:build windows

package process

import (
	"fmt"
	"os/exec"
)

func shellCommand(command string) *exec.Cmd {
	return exec.Command("cmd", "/C", command)
}

func killTree(cmd *exec.Cmd) {
	exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", cmd.Process.Pid)).Run()
}

func setSysProcAttr(cmd *exec.Cmd) {}