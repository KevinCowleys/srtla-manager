//go:build !windows

package process

import (
	"syscall"
	"time"
)

func (p *Process) Stop() error {
	p.mu.Lock()
	if p.cmd == nil || p.state == StateStopped {
		p.mu.Unlock()
		return nil
	}
	cmd := p.cmd
	cancel := p.cancel
	p.state = StateStopped
	p.mu.Unlock()

	if cmd.Process != nil {
		cmd.Process.Signal(syscall.SIGTERM)

		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			cmd.Process.Kill()
		}
	}

	if cancel != nil {
		cancel()
	}

	return nil
}

func (p *Process) Signal(sig syscall.Signal) error {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.cmd == nil || p.cmd.Process == nil {
		return nil
	}
	return p.cmd.Process.Signal(sig)
}
