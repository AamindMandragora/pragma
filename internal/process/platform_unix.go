//go:build !windows

package process

import (
	"os/exec"
	"syscall"
)

func shellCommand(command string) *exec.Cmd {
	return exec.Command("sh", "-c", "stdbuf -oL -eL "+command)
}

func pythonCommand(path string) *exec.Cmd {
	return exec.Command("python", "-u", path)
}

func killTree(cmd *exec.Cmd) {
	syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
}

func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
