package vpn

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"openconnectmulti/internal/vault"
)

type State string

const (
	StateDisconnected State = "disconnected"
	StateConnecting   State = "connecting"
	StateConnected    State = "connected"
	StateStopping     State = "stopping"
)

type Status struct {
	State       State     `json:"state"`
	ProfileID   string    `json:"profile_id,omitempty"`
	ProfileName string    `json:"profile_name,omitempty"`
	PID         int       `json:"pid,omitempty"`
	StartedAt   time.Time `json:"started_at,omitempty"`
	LastError   string    `json:"last_error,omitempty"`
	Command     string    `json:"command,omitempty"`
}

type LogEntry struct {
	Time    time.Time `json:"time"`
	Message string    `json:"message"`
}

type Manager struct {
	mu      sync.Mutex
	cmd     *exec.Cmd
	done    chan error
	status  Status
	logs    []LogEntry
	maxLogs int
}

func NewManager() *Manager {
	return &Manager{
		status:  Status{State: StateDisconnected},
		maxLogs: 500,
	}
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *Manager) Logs() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogEntry, len(m.logs))
	copy(out, m.logs)
	return out
}

func (m *Manager) Connect(profile vault.Profile, settings vault.Settings) error {
	if strings.TrimSpace(profile.Server) == "" {
		return errors.New("server is required")
	}
	if strings.TrimSpace(profile.Password) == "" {
		return errors.New("password is required")
	}

	if m.isRunning() {
		if err := m.Disconnect(); err != nil {
			return err
		}
	}

	binary := ResolveOpenConnectPath(settings.OpenConnectPath)
	if strings.Contains(strings.ToLower(filepath.Base(binary)), "openconnect-gui") {
		return errors.New("openconnect-gui.exe cannot receive a stored password; set OpenConnect path to openconnect.exe")
	}
	args := buildArgs(profile)
	cmd := exec.Command(binary, args...)
	configureCommand(cmd)

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := cmd.Start(); err != nil {
		m.appendLog(fmt.Sprintf("failed to start %s: %v", binary, err))
		return err
	}

	_, _ = io.WriteString(stdin, profile.Password+"\n")
	_ = stdin.Close()

	m.mu.Lock()
	m.cmd = cmd
	m.done = make(chan error, 1)
	m.status = Status{
		State:       StateConnecting,
		ProfileID:   profile.ID,
		ProfileName: profile.Name,
		PID:         cmd.Process.Pid,
		StartedAt:   time.Now(),
		Command:     sanitizeCommand(binary, args),
	}
	m.appendLogLocked("starting " + m.status.Command)
	done := m.done
	m.mu.Unlock()

	go m.pipeLogs(stdout)
	go m.pipeLogs(stderr)
	go func() {
		err := cmd.Wait()
		done <- err
		close(done)

		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd != cmd {
			return
		}
		m.cmd = nil
		m.done = nil
		last := ""
		if err != nil {
			last = err.Error()
			m.appendLogLocked("openconnect exited: " + err.Error())
		} else {
			m.appendLogLocked("openconnect exited")
		}
		m.status = Status{
			State:       StateDisconnected,
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			LastError:   last,
			Command:     sanitizeCommand(binary, args),
		}
	}()

	go func() {
		time.Sleep(1200 * time.Millisecond)
		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd == cmd && m.status.State == StateConnecting {
			m.status.State = StateConnected
			m.appendLogLocked("connection process is running")
		}
	}()

	return nil
}

func (m *Manager) Disconnect() error {
	m.mu.Lock()
	cmd := m.cmd
	done := m.done
	if cmd == nil || cmd.Process == nil {
		m.status.State = StateDisconnected
		m.mu.Unlock()
		return nil
	}
	m.status.State = StateStopping
	m.appendLogLocked("stopping openconnect")
	m.mu.Unlock()

	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F").Run()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}

	select {
	case <-done:
		return nil
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
		return nil
	}
}

func (m *Manager) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.Process != nil
}

func (m *Manager) pipeLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			m.appendLog(line)
		}
	}
	if err := scanner.Err(); err != nil {
		m.appendLog("read log error: " + err.Error())
	}
}

func (m *Manager) appendLog(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendLogLocked(message)
}

func (m *Manager) appendLogLocked(message string) {
	m.logs = append(m.logs, LogEntry{Time: time.Now(), Message: message})
	if len(m.logs) > m.maxLogs {
		m.logs = m.logs[len(m.logs)-m.maxLogs:]
	}
}

func buildArgs(profile vault.Profile) []string {
	args := []string{"--passwd-on-stdin"}
	if profile.Username != "" {
		args = append(args, "--user", profile.Username)
	}
	if profile.AuthGroup != "" {
		args = append(args, "--authgroup", profile.AuthGroup)
	}
	if profile.Protocol != "" {
		args = append(args, "--protocol", profile.Protocol)
	}
	if profile.UserAgent != "" {
		args = append(args, "--useragent", profile.UserAgent)
	}
	if profile.ServerCert != "" {
		args = append(args, "--servercert", profile.ServerCert)
	}
	if profile.NoCertCheck {
		args = append(args, "--no-cert-check")
	}
	for _, arg := range profile.ExtraArgs {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			args = append(args, arg)
		}
	}
	args = append(args, profile.Server)
	return args
}

func sanitizeCommand(binary string, args []string) string {
	parts := []string{binary}
	parts = append(parts, args...)
	return strings.Join(parts, " ")
}
