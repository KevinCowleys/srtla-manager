//go:build windows

package process

import (
	"fmt"
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
		done := make(chan struct{})
		go func() {
			cmd.Wait()
			close(done)
		}()

		cmd.Process.Kill()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
		}
	}

	if cancel != nil {
		cancel()
	}

	return nil
}

func (p *Process) Signal(sig int) error {
	return fmt.Errorf("signals not supported on Windows")
}
