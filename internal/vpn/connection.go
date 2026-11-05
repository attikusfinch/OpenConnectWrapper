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
	State       State          `json:"state"`
	ProfileID   string         `json:"profile_id,omitempty"`
	ProfileName string         `json:"profile_name,omitempty"`
	PID         int            `json:"pid,omitempty"`
	StartedAt   time.Time      `json:"started_at,omitempty"`
	LastError   string         `json:"last_error,omitempty"`
	Command     string         `json:"command,omitempty"`
	Multi       bool           `json:"multi"`
	Network     *NetworkStatus `json:"network,omitempty"`
}

type LogEntry struct {
	Time        time.Time `json:"time"`
	Message     string    `json:"message"`
	ProfileID   string    `json:"profile_id,omitempty"`
	ProfileName string    `json:"profile_name,omitempty"`
}

type connection struct {
	managed      bool
	network      *networkSession
	cleanupError error
	operationMu  sync.Mutex
	mu           sync.Mutex
	cmd          *exec.Cmd
	done         chan error
	status       Status
	logs         []LogEntry
	maxLogs      int
}

func newConnection() *connection {
	return &connection{
		status:  Status{State: StateDisconnected},
		maxLogs: 500,
	}
}

func (m *connection) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.status
}

func (m *connection) Logs() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]LogEntry, len(m.logs))
	copy(out, m.logs)
	return out
}

func (m *connection) Connect(profile vault.Profile, settings vault.Settings) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if strings.TrimSpace(profile.Server) == "" {
		return errors.New("server is required")
	}
	if strings.TrimSpace(profile.Password) == "" {
		return errors.New("password is required")
	}

	if m.isRunning() {
		if err := m.disconnect(); err != nil {
			return err
		}
	}

	binary := ResolveOpenConnectPath(settings.OpenConnectPath)
	if strings.Contains(strings.ToLower(filepath.Base(binary)), "openconnect-gui") {
		return errors.New("openconnect-gui.exe cannot receive a stored password; set OpenConnect path to openconnect.exe")
	}
	args := buildArgs(profile)
	if m.network != nil {
		args = append(args[:len(args)-1], "--interface", m.network.iface, "--script", m.network.script, "--disable-ipv6", profile.Server)
	}
	cmd := exec.Command(binary, args...)
	if m.network != nil {
		cmd.Env = append(os.Environ(), m.network.env...)
	}
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
	if m.network != nil {
		if err := m.network.recordProcess(cmd.Process.Pid); err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("record tunnel process: %w", err)
		}
	}

	_, _ = io.WriteString(stdin, profile.Password+"\n")
	_ = stdin.Close()

	m.mu.Lock()
	m.cmd = cmd
	m.done = make(chan error, 1)
	m.status = Status{
		Multi:       m.managed,
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

	var readers sync.WaitGroup
	readers.Add(2)
	go func() { defer readers.Done(); m.pipeLogs(stdout) }()
	go func() { defer readers.Done(); m.pipeLogs(stderr) }()
	go func() {
		err := cmd.Wait()
		readers.Wait()
		defer func() { done <- err; close(done) }()
		var cleanupErr error
		if m.network != nil {
			cleanupErr = m.network.cleanup()
			if cleanupErr == nil {
				_ = os.Remove(m.network.state + ".process")
			}
		}

		m.mu.Lock()
		defer m.mu.Unlock()
		if m.cmd != cmd {
			return
		}
		m.cmd = nil
		m.done = nil
		m.cleanupError = cleanupErr
		last := m.status.LastError
		if cleanupErr != nil {
			last = cleanupErr.Error()
			m.appendLogLocked(last)
		}
		if err != nil && m.status.State != StateStopping {
			if last == "" {
				last = err.Error()
			}
			m.appendLogLocked("openconnect exited: " + err.Error())
		} else {
			m.appendLogLocked("openconnect exited")
		}
		m.status = Status{
			Multi:       m.managed,
			State:       StateDisconnected,
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			LastError:   last,
			Command:     sanitizeCommand(binary, args),
		}
	}()

	if m.network != nil {
		go m.watchNetwork(cmd, done)
	}

	return nil
}

func (m *connection) Disconnect() error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	return m.disconnect()
}

func (m *connection) disconnect() error {
	m.mu.Lock()
	cmd := m.cmd
	done := m.done
	if cmd == nil || cmd.Process == nil {
		m.status.State = StateDisconnected
		previous := m.cleanupError
		m.mu.Unlock()
		if previous != nil && m.network != nil {
			err := m.network.cleanup()
			m.mu.Lock()
			m.cleanupError = err
			m.mu.Unlock()
			return err
		}
		return nil
	}
	m.status.State = StateStopping
	m.appendLogLocked("stopping openconnect")
	m.mu.Unlock()

	if runtime.GOOS == "windows" {
		kill := exec.Command("taskkill", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F")
		configureCommand(kill)
		_ = kill.Run()
	} else {
		_ = cmd.Process.Signal(os.Interrupt)
	}

	select {
	case <-done:
		m.mu.Lock()
		defer m.mu.Unlock()
		return m.cleanupError
	case <-time.After(50 * time.Second):
		_ = cmd.Process.Kill()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			return errors.New("openconnect did not stop; refusing to start another connection")
		}
		return nil
	}
}

func (m *connection) isRunning() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.cmd != nil && m.cmd.Process != nil
}

func (m *connection) pipeLogs(r io.Reader) {
	scanner := bufio.NewScanner(r)
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			m.appendLog(line)
			if !m.managed && strings.Contains(line, "Configured as ") {
				m.mu.Lock()
				if m.status.State == StateConnecting {
					m.status.State = StateConnected
				}
				m.mu.Unlock()
			}
		}
	}
	if err := scanner.Err(); err != nil {
		m.appendLog("read log error: " + err.Error())
	}
}

func (m *connection) watchNetwork(cmd *exec.Cmd, done <-chan error) {
	ticker := time.NewTicker(300 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			network, err := m.network.read()
			if err != nil {
				continue
			}
			m.mu.Lock()
			if m.cmd != cmd || m.status.State == StateStopping {
				m.mu.Unlock()
				return
			}
			m.status.Network = &network
			if network.Error != "" {
				m.status.LastError = network.Error
			}
			if network.Ready {
				m.status.State = StateConnected
			} else {
				m.status.State = StateConnecting
			}
			m.mu.Unlock()
			if network.Error != "" {
				_ = cmd.Process.Kill()
				return
			}
		}
	}
}

func (m *connection) appendLog(message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendLogLocked(message)
}

func (m *connection) appendLogLocked(message string) {
	m.logs = append(m.logs, LogEntry{Time: time.Now(), Message: message, ProfileID: m.status.ProfileID, ProfileName: m.status.ProfileName})
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
