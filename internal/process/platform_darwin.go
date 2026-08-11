//go:build darwin

package process

import (
	"os/exec"
	"syscall"
)

// stdbuf is GNU coreutils and isn't on stock macOS (homebrew installs it as gstdbuf).
// uses it when present so output streams line by line, otherwise runs the command bare
func shellCommand(command string) *exec.Cmd {
	for _, name := range []string{"stdbuf", "gstdbuf"} {
		if path, err := exec.LookPath(name); err == nil {
			return exec.Command("sh", "-c", path+" -oL -eL "+command)
		}
	}
	return exec.Command("sh", "-c", command)
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

func preloadEnv(libPath string) string {
    return "DYLD_INSERT_LIBRARIES=" + libPath
}

func cLibName() string {
    return "pragma_lib.dylib"
}