package process

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"sync"
	"time"
)

type State string

const (
	StateStopped  State = "stopped"
	StateStarting State = "starting"
	StateRunning  State = "running"
	StateError    State = "error"
)

type LogLine struct {
	Timestamp time.Time
	Source    string
	Line      string
}

type Process struct {
	mu          sync.RWMutex
	name        string
	cmd         *exec.Cmd
	state       State
	lastError   string
	startTime   time.Time
	logCallback func(LogLine)
	cancel      context.CancelFunc
}

func New(name string) *Process {
	return &Process{
		name:  name,
		state: StateStopped,
	}
}

func (p *Process) SetLogCallback(cb func(LogLine)) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.logCallback = cb
}

func (p *Process) Start(cmdPath string, args ...string) error {
	p.mu.Lock()
	if p.state == StateRunning || p.state == StateStarting {
		p.mu.Unlock()
		return fmt.Errorf("process already running")
	}
	p.state = StateStarting
	p.lastError = ""
	p.mu.Unlock()

	// Log the command being started
	fullCmd := cmdPath
	if len(args) > 0 {
		fullCmd = fmt.Sprintf("%s %s", cmdPath, formatArgs(args))
	}
	p.logOutput(LogLine{
		Timestamp: time.Now(),
		Source:    p.name,
		Line:      fmt.Sprintf("[STARTING] %s", fullCmd),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, cmdPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		p.setState(StateError, err.Error())
		cancel()
		return err
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		p.setState(StateError, err.Error())
		cancel()
		return err
	}

	if err := cmd.Start(); err != nil {
		p.setState(StateError, err.Error())
		cancel()
		return err
	}

	p.mu.Lock()
	p.cmd = cmd
	p.cancel = cancel
	p.state = StateRunning
	p.startTime = time.Now()
	p.mu.Unlock()

	go p.readOutput(stdout, p.name)
	go p.readOutput(stderr, p.name)

	go func() {
		err := cmd.Wait()
		p.mu.Lock()
		defer p.mu.Unlock()
		// Only update state if this is still the active command.
		// A new Start() may have replaced p.cmd while we were waiting.
		if p.cmd != cmd {
			return
		}
		if err != nil && p.state != StateStopped {
			p.state = StateError
			p.lastError = err.Error()
		} else if p.state != StateStopped {
			p.state = StateStopped
		}
		p.cmd = nil
	}()

	return nil
}

// Stop() and Signal() are implemented in signals_unix.go and signals_windows.go

func (p *Process) State() State {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.state
}

func (p *Process) LastError() string {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.lastError
}

func (p *Process) Uptime() time.Duration {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.state != StateRunning {
		return 0
	}
	return time.Since(p.startTime)
}

func (p *Process) setState(state State, errMsg string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state = state
	p.lastError = errMsg
}

func (p *Process) readOutput(r io.Reader, source string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		p.mu.RLock()
		cb := p.logCallback
		p.mu.RUnlock()
		if cb != nil {
			cb(LogLine{
				Timestamp: time.Now(),
				Source:    source,
				Line:      line,
			})
		}
	}
}

func (p *Process) logOutput(line LogLine) {
	p.mu.RLock()
	cb := p.logCallback
	p.mu.RUnlock()
	if cb != nil {
		cb(line)
	}
}

// formatArgs joins command arguments, quoting those with spaces
func formatArgs(args []string) string {
	formatted := make([]string, 0, len(args))
	for _, arg := range args {
		if len(arg) == 0 {
			formatted = append(formatted, `""`)
		} else if len(arg) > 50 {
			// Truncate long arguments like file paths
			formatted = append(formatted, arg[:47]+"...")
		} else if strings.ContainsAny(arg, " \t") {
			formatted = append(formatted, fmt.Sprintf(`"%s"`, arg))
		} else {
			formatted = append(formatted, arg)
		}
	}
	return strings.Join(formatted, " ")
}
