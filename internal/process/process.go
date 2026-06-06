package process

import (
	"context"
	"os/exec"
	"time"

	"github.com/google/uuid"
)

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

type ProcessResult struct {
	Status string
	ExitCode int
	Stdout *OutputBuffer
	Stderr *OutputBuffer
}

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

func (p *Process) Wait() ProcessResult {
	<-p.Done
	return ProcessResult{Status: p.Status, ExitCode: p.ExitCode, Stdout: p.Stdout, Stderr: p.Stderr};
}

func (p *Process) IsRunning() bool {
	select {
	case <-p.Done:
		return false
	default:
		return true
	}
}