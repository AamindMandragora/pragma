package process

import (
	"context"

	"github.com/google/uuid"
)

// Process tracks the state and output buffers for a running command.
type Process struct {
	Id       string
	Pid      int
	Status   string
	ExitCode int
	Stdout   *OutputBuffer
	Stderr   *OutputBuffer
	Cleanup  context.CancelFunc
	Done     chan struct{}
}

// the result of a process will be the status, exit code, and stdout/stderr buffers
type ProcessResult struct {
	Status   string
	ExitCode int
	Stdout   *OutputBuffer
	Stderr   *OutputBuffer
}

// creates a new process by initializing buffers, then fills in fields
func NewProcess() (*Process, error) {
	stdout, err := NewOutputBuffer()
	if err != nil {
		return nil, err
	}
	stderr, err := NewOutputBuffer()
	if err != nil {
		return nil, err
	}
	var p = &Process{Id: uuid.New().String(), Status: "running", Stdout: stdout, Stderr: stderr, Done: make(chan struct{})}
	return p, nil
}

// blocks until we get the empty packet in the done channel, then returns a process result made from process fields
func (p *Process) Wait() ProcessResult {
	<-p.Done
	return ProcessResult{Status: p.Status, ExitCode: p.ExitCode, Stdout: p.Stdout, Stderr: p.Stderr}
}
