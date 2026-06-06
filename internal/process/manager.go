package process

import (
	"context"
	"errors"
	"sync"
	"time"
)

type Manager struct {
	Processes map[string]*Process
	M         sync.Mutex
}

func NewManager() *Manager {
	return &Manager{Processes: make(map[string]*Process), M: sync.Mutex{}}
}

func (m *Manager) Start(command string, timeout time.Duration) (*Process, error) {
	p, err := NewProcess(command)
	if err != nil {
		return nil, err
	}

	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}

	p.Cleanup = cancel
	cmd := shellCommand(command)
	cmd.Stdout = p.Stdout
	cmd.Stderr = p.Stderr
	setSysProcAttr(cmd)

	if err := cmd.Start(); err != nil {
		cancel()
		p.Stdout.Close()
		p.Stderr.Close()
		return nil, err
	}

	p.Pid = cmd.Process.Pid

	m.M.Lock()
	m.Processes[p.Id] = p
	m.M.Unlock()

	go func() {
		waitCh := make(chan error, 1)
		go func() {
			waitCh <- cmd.Wait()
		}()

		select {
		case <-ctx.Done():
			cmd.Process.Kill()
			killTree(cmd)
			select {
			case <-waitCh:
			case <-time.After(2 * time.Second):
			}
		case <-waitCh:
		}

		if p.Stdout.partial != "" {
			p.Stdout.Write([]byte("\n"))
		}
		if p.Stderr.partial != "" {
			p.Stderr.Write([]byte("\n"))
		}
		if cmd.ProcessState != nil {
			p.ExitCode = cmd.ProcessState.ExitCode()
		}

		switch ctx.Err() {
		case context.DeadlineExceeded:
			p.Status = "timeout"
		case context.Canceled:
			p.Status = "killed"
		case nil:
			p.Status = "exited"
		}
		close(p.Done)
	}()

	return p, nil
}

func (m *Manager) Get(id string) *Process {
	m.M.Lock()
	defer m.M.Unlock()
	return m.Processes[id]
}

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
