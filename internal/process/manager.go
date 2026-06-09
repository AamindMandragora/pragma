package process

import (
	"context"
	"errors"
	"sync"
	"time"
)

// process manager will hold a list of processes and a mutex
type Manager struct {
	Processes map[string]*Process
	M         sync.Mutex
}

// creates a new manager
func NewManager() *Manager {
	return &Manager{Processes: make(map[string]*Process), M: sync.Mutex{}}
}

// creates a new process to run the given command, returns a pointer to it
func (m *Manager) Start(command string, timeout time.Duration) (*Process, error) {
	// creates the new process
	p, err := NewProcess(command)
	if err != nil {
		return nil, err
	}

	// creates a cancel function based on how long the timeout is
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	// makes the cancel function the process cleanup function
	p.Cleanup = cancel
	// depending on OS creates the command
	cmd := shellCommand(command)
	cmd.Stdout = p.Stdout
	cmd.Stderr = p.Stderr
	// depending on OS set the system process attributes
	setSysProcAttr(cmd)

	// starts the command, cleans up if fails
	if err := cmd.Start(); err != nil {
		cancel()
		p.Stdout.Close()
		p.Stderr.Close()
		return nil, err
	}

	p.Pid = cmd.Process.Pid

	// locks mutex before accessing processes map
	m.M.Lock()
	m.Processes[p.Id] = p
	m.M.Unlock()

	// creates a goroutine that monitors the process
	go func() {
		// makes a channel and a goroutine that will send it a message once the command has finished running
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- cmd.Wait()
		}()

		select {
		// if the cancel was called then kill the process and any subprocesses (according to OS)
		case <-ctx.Done():
			cmd.Process.Kill()
			killTree(cmd)
			select {
			// waits two seconds for process to be removed from memory
			case <-waitCh:
			case <-time.After(2 * time.Second):
			}
		// if the command finished normally then do nothing
		case <-waitCh:
		}

		// flush buffers by sending a newline
		if p.Stdout.partial != "" {
			p.Stdout.Write([]byte("\n"))
		}
		if p.Stderr.partial != "" {
			p.Stderr.Write([]byte("\n"))
		}
		// gets the exit code from the process state
		if cmd.ProcessState != nil {
			p.ExitCode = cmd.ProcessState.ExitCode()
		}

		// changes status based on whether we killed the process or it timed out, otherwise it exited normally
		switch ctx.Err() {
		case context.DeadlineExceeded:
			p.Status = "timeout"
		case context.Canceled:
			p.Status = "killed"
		case nil:
			p.Status = "exited"
		}
		// closes the done channel
		close(p.Done)
	}()

	// returns a pointer to the process and nil error
	return p, nil
}

// gets a process owned by the manager by its id
func (m *Manager) Get(id string) *Process {
	m.M.Lock()
	defer m.M.Unlock()
	return m.Processes[id]
}

// kills a process owned by the manager by its id
func (m *Manager) Kill(id string) error {
	m.M.Lock()
	p, ok := m.Processes[id]
	m.M.Unlock()
	if !ok {
		return errors.New("Process not found")
	}
	p.Cleanup()
	return nil
}
