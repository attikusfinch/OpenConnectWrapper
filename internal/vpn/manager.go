package vpn

import (
	"errors"
	"fmt"
	"openconnectmulti/internal/vault"
	"sort"
	"sync"
)

// Manager serializes changes to the collection; every tunnel owns its process.
type Manager struct {
	operationMu     sync.Mutex
	mu              sync.Mutex
	connections     map[string]*connection
	last            string
	networkDir      string
	persistentDir   string
	networkPrepared bool
}

func NewManager() *Manager { return &Manager{connections: make(map[string]*connection)} }

func NewManagerWithNetworkDir(dir string) *Manager {
	m := NewManager()
	m.persistentDir = dir
	return m
}

func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	last := Status{State: StateDisconnected}
	if c := m.connections[m.last]; c != nil {
		last = c.Status()
		if last.State != StateDisconnected {
			return last
		}
	}
	for _, c := range m.connections {
		status := c.Status()
		if status.State != StateDisconnected {
			return status
		}
	}
	return last
}

func (m *Manager) Statuses() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make([]Status, 0, len(m.connections))
	for _, c := range m.connections {
		result = append(result, c.Status())
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ProfileID < result[j].ProfileID })
	return result
}

func (m *Manager) Logs() []LogEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := []LogEntry{}
	for _, c := range m.connections {
		result = append(result, c.Logs()...)
	}
	sort.SliceStable(result, func(i, j int) bool { return result[i].Time.Before(result[j].Time) })
	if len(result) > 1500 {
		result = result[len(result)-1500:]
	}
	return result
}

func (m *Manager) Connect(p vault.Profile, settings vault.Settings) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if p.ID == "" || p.Server == "" || p.Password == "" {
		return errors.New("profile ID, server and password are required")
	}
	if err := ValidateNetworkOptions(p.Routes, p.DNSDomains); err != nil {
		return err
	}
	if settings.MultiMode {
		if err := validateManagedArgs(p.ExtraArgs); err != nil {
			return err
		}
	}
	connections := m.snapshot()
	if c := connections[p.ID]; c != nil && c.isRunning() {
		if settings.MultiMode && (c.managed != settings.MultiMode || (c.network != nil && c.network.full != (settings.InternetProfile == p.ID))) {
			return errors.New("disconnect this profile before changing its routing mode")
		}
		if settings.MultiMode {
			return nil
		}
	}
	if !settings.MultiMode {
		if err := m.disconnectAll(); err != nil {
			return err
		}
	} else {
		for _, c := range connections {
			if c.isRunning() && !c.managed {
				return errors.New("disconnect the single VPN before starting Multi mode")
			}
		}
	}
	c := newConnection()
	c.managed = settings.MultiMode
	c.status = Status{State: StateDisconnected, ProfileID: p.ID, ProfileName: p.Name, Multi: settings.MultiMode}
	if settings.MultiMode {
		var err error
		err = m.ensureNetworks()
		if err != nil {
			return err
		}
		c.network, err = prepareNetwork(m.networkDir, p, settings.InternetProfile == p.ID)
		if err != nil {
			return err
		}
	}
	m.mu.Lock()
	m.connections[p.ID] = c
	m.last = p.ID
	m.mu.Unlock()
	err := c.Connect(p, settings)
	if err != nil {
		c.mu.Lock()
		c.status.LastError = err.Error()
		c.appendLogLocked(err.Error())
		c.mu.Unlock()
	}
	return err
}

func (m *Manager) DisconnectProfile(id string) error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	if c := m.snapshot()[id]; c != nil {
		return c.Disconnect()
	}
	return nil
}

func (m *Manager) Disconnect() error {
	m.operationMu.Lock()
	defer m.operationMu.Unlock()
	return m.disconnectAll()
}

func (m *Manager) disconnectAll() error {
	var problems []error
	for id, c := range m.snapshot() {
		if err := c.Disconnect(); err != nil {
			problems = append(problems, fmt.Errorf("%s: %w", id, err))
		}
	}
	return errors.Join(problems...)
}

func (m *Manager) snapshot() map[string]*connection {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]*connection, len(m.connections))
	for id, c := range m.connections {
		result[id] = c
	}
	return result
}
