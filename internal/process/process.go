package process

import (
	"context"
	"os/exec"
	"time"

	"github.com/google/uuid"
)

// processes will have an internal uuid, a pid, a name, a command, a start time, a status, an exit code, stdout and stderr buffers, a cleanup function, and a channel that only holds an eleemnt when the process is done
type Process struct {
	Id string
	Pid int
	Name string
	Command *exec.Cmd
	Start time.Time
	Status string
	ExitCode int
	Stdout *OutputBuffer
	Stderr *OutputBuffer
	Cleanup context.CancelFunc
	Done chan struct{}
}

// the result of a process will be the status, exit code, and stdout/stderr buffers
type ProcessResult struct {
	Status string
	ExitCode int
	Stdout *OutputBuffer
	Stderr *OutputBuffer
}

// creates a new process by initializing buffers, then fills in fields
func NewProcess(command string) (*Process, error) {
	stdout, err := NewOutputBuffer(100)
	if err != nil {
		return nil, err
	}
	stderr, err := NewOutputBuffer(100)
	if err != nil {
		return nil, err
	}
	var p = &Process{Id: uuid.New().String(), Name: command, Start: time.Now(), Status: "running", Stdout: stdout, Stderr: stderr, Done: make(chan struct{})}
	return p, nil
}

// blocks until we get the empty packet in the done channel, then returns a process result made from process fields
func (p *Process) Wait() ProcessResult {
	<-p.Done
	return ProcessResult{Status: p.Status, ExitCode: p.ExitCode, Stdout: p.Stdout, Stderr: p.Stderr};
}

// checks if there's something in the done channel, has stopped if true
func (p *Process) IsRunning() bool {
	select {
	case <-p.Done:
		return false
	default:
		return true
	}
}