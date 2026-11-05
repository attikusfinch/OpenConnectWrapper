package vpn

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"openconnectmulti/internal/vault"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"time"
)

//go:embed network/multi.js network/multi.ps1
var networkFiles embed.FS

type NetworkStatus struct {
	Ready      bool     `json:"ready"`
	Error      string   `json:"error,omitempty"`
	Interface  string   `json:"interface"`
	Address    string   `json:"address,omitempty"`
	Routes     []string `json:"routes,omitempty"`
	DNSServers []string `json:"dns_servers,omitempty"`
	DNSDomains []string `json:"dns_domains,omitempty"`
	Full       bool     `json:"full"`
	Warning    string   `json:"warning,omitempty"`
}

type networkSession struct {
	root, script, powershell, state, iface string
	env                                    []string
	full                                   bool
}

var dnsDomain = regexp.MustCompile(`^\.?[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func ValidateNetworkOptions(routes, domains []string) error {
	for _, r := range routes {
		prefix, err := netip.ParsePrefix(r)
		if err != nil || !prefix.Addr().Is4() || prefix.Bits() <= 1 {
			return fmt.Errorf("invalid IPv4 work network %q; use CIDR, for example 10.110.16.0/20", r)
		}
	}
	for _, d := range domains {
		if !dnsDomain.MatchString(d) || strings.Contains(d, "..") {
			return fmt.Errorf("invalid DNS domain %q", d)
		}
	}
	return nil
}

func validateManagedArgs(args []string) error {
	for _, arg := range args {
		// Config files and custom scripts can silently replace Multi's routing.
		for _, option := range []string{"--script", "--interface", "--config", "--script-tun", "--disable-ipv6", "--dtls-local-port"} {
			if arg == option || strings.HasPrefix(arg, option+"=") {
				return fmt.Errorf("%s is managed by Multi mode; remove it from extra arguments", option)
			}
		}
		if strings.HasPrefix(arg, "-s") || strings.HasPrefix(arg, "-i") || strings.HasPrefix(arg, "-S") {
			return errors.New("custom interface/script arguments are not supported in Multi mode")
		}
	}
	return nil
}

func createNetworkDir() (string, error) {
	dir, err := os.MkdirTemp("", "openconnectmulti-network-")
	if err != nil {
		return "", err
	}
	return dir, writeNetworkFiles(dir)
}

func writeNetworkFiles(dir string) error {
	for _, name := range []string{"multi.js", "multi.ps1"} {
		data, err := networkFiles.ReadFile("network/" + name)
		if err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}

func baseNetworkSession(root, id string) *networkSession {
	n := &networkSession{root: root, script: filepath.Join(root, "multi.js"), powershell: filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe"), state: filepath.Join(root, id+".json"), iface: "OCM-" + id}
	n.env = []string{"OCM_NETWORK_ROOT=" + root, "OCM_STATE_FILE=" + n.state, "OCM_INTERFACE=" + n.iface, "OCM_POWERSHELL=" + n.powershell}
	return n
}

func (m *Manager) ensureNetworks() error {
	if m.networkPrepared {
		return nil
	}
	if m.persistentDir == "" {
		dir, err := createNetworkDir()
		if err != nil {
			return err
		}
		m.networkDir = dir
	} else {
		if err := os.MkdirAll(m.persistentDir, 0700); err != nil {
			return err
		}
		m.networkDir = m.persistentDir
		if err := writeNetworkFiles(m.networkDir); err != nil {
			return err
		}
		entries, err := os.ReadDir(m.networkDir)
		if err != nil {
			return err
		}
		ids := map[string]bool{}
		pattern := regexp.MustCompile(`^([0-9a-f]{16})\.json(?:\.process)?$`)
		for _, entry := range entries {
			if match := pattern.FindStringSubmatch(entry.Name()); match != nil {
				ids[match[1]] = true
			}
		}
		for id := range ids {
			n := baseNetworkSession(m.networkDir, id)
			if err := n.recoverProcess(); err != nil {
				return err
			}
			if err := n.cleanup(); err != nil {
				return err
			}
			_ = os.Remove(n.state)
			_ = os.Remove(n.state + ".process")
		}
	}
	m.networkPrepared = true
	return nil
}

type processRecord struct {
	PID      int    `json:"pid"`
	Identity uint64 `json:"identity"`
}

func (n *networkSession) recordProcess(pid int) error {
	identity, err := processIdentity(pid)
	if err != nil {
		return err
	}
	data, _ := json.Marshal(processRecord{pid, identity})
	return os.WriteFile(n.state+".process", data, 0600)
}

func (n *networkSession) recoverProcess() error {
	data, err := os.ReadFile(n.state + ".process")
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var record processRecord
	if err := json.Unmarshal(data, &record); err != nil {
		return err
	}
	identity, err := processIdentity(record.PID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if identity != record.Identity {
		return nil
	}
	cmd := exec.Command("taskkill", "/PID", fmt.Sprint(record.PID), "/T", "/F")
	configureCommand(cmd)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("stop orphaned managed tunnel: %w: %s", err, out)
	}
	return nil
}

func prepareNetwork(root string, p vault.Profile, full bool) (*networkSession, error) {
	if runtime.GOOS != "windows" {
		return nil, errors.New("Multi networking currently requires Windows")
	}
	sum := sha256.Sum256([]byte(p.ID))
	id := hex.EncodeToString(sum[:8])
	routes, _ := json.Marshal(p.Routes)
	domains, _ := json.Marshal(p.DNSDomains)
	n := baseNetworkSession(root, id)
	n.full = full
	n.env = append(n.env, "OCM_FULL="+fmt.Sprint(full), "OCM_ROUTES="+string(routes), "OCM_DNS_DOMAINS="+string(domains))
	if _, err := os.Stat(n.state); err == nil {
		if err := n.cleanup(); err != nil {
			return nil, err
		}
		if err := os.Remove(n.state); err != nil {
			return nil, err
		}
	}
	return n, nil
}

func (n *networkSession) read() (NetworkStatus, error) {
	var state NetworkStatus
	b, err := os.ReadFile(n.state)
	if err != nil {
		return state, err
	}
	err = json.Unmarshal(b, &state)
	return state, err
}

func (n *networkSession) cleanup() error {
	if _, err := os.Stat(n.state); os.IsNotExist(err) {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, n.powershell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(n.root, "multi.ps1"))
	cmd.Env = append(append(os.Environ(), n.env...), "reason=disconnect")
	configureCommand(cmd)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("network cleanup: %w: %s", err, strings.TrimSpace(string(output)))
	}
	return nil
}
