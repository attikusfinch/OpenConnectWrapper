package vpn

import (
	"encoding/json"
	"openconnectmulti/internal/vault"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestMultiProcessIsolation(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows Multi networking")
	}
	t.Setenv("OCM_FAKE_VPN", "1")
	exe, _ := os.Executable()
	m := NewManager()
	t.Cleanup(func() { _ = m.Disconnect() })
	settings := vault.Settings{OpenConnectPath: exe, MultiMode: true, InternetProfile: "internet"}
	for _, id := range []string{"internet", "work-a", "work-b"} {
		if err := m.Connect(vault.Profile{ID: id, Name: id, Server: "test.invalid", Password: "test"}, settings); err != nil {
			t.Fatal(err)
		}
	}
	statuses := m.Statuses()
	if len(statuses) != 3 {
		t.Fatalf("expected three processes: %+v", statuses)
	}
	pids := map[int]bool{}
	for _, s := range statuses {
		if s.PID == 0 || pids[s.PID] {
			t.Fatal("missing or shared process")
		}
		pids[s.PID] = true
	}
	if err := m.Connect(vault.Profile{ID: "work-b", Name: "work-b", Server: "test.invalid", Password: "test"}, settings); err != nil {
		t.Fatal(err)
	}
	if err := m.DisconnectProfile("work-a"); err != nil {
		t.Fatal(err)
	}
	for _, s := range m.Statuses() {
		if s.ProfileID == "work-a" {
			if s.State != StateDisconnected {
				t.Fatal("profile still running")
			}
		} else if s.PID == 0 {
			t.Fatal("unrelated tunnel stopped")
		}
	}
	settings.MultiMode = false
	if err := m.Connect(vault.Profile{ID: "internet", Name: "internet", Server: "test.invalid", Password: "test"}, settings); err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, s := range m.Statuses() {
		if s.PID != 0 {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("exclusive switch left %d processes", active)
	}
}

func TestManagedNetworkValidation(t *testing.T) {
	for _, route := range []string{"0.0.0.0/0", "0.0.0.0/1", "::/0", "10.0.0.0/33", "nonsense"} {
		if ValidateNetworkOptions([]string{route}, nil) == nil {
			t.Fatalf("accepted %q", route)
		}
	}
	for _, arg := range []string{"--script=x", "-sother.js", "--interface", "--config=foo", "--script-tun"} {
		if validateManagedArgs([]string{arg}) == nil {
			t.Fatalf("accepted %q", arg)
		}
	}
	if err := ValidateNetworkOptions([]string{"10.110.16.0/20"}, []string{"corp.example.com"}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverRecordedFakeProcess(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows process recovery")
	}
	t.Setenv("OCM_FAKE_VPN", "1")
	exe, _ := os.Executable()
	dir := t.TempDir()
	first := NewManagerWithNetworkDir(dir)
	t.Cleanup(func() { _ = first.Disconnect() })
	p := vault.Profile{ID: "orphan", Server: "test.invalid", Password: "test"}
	settings := vault.Settings{MultiMode: true, OpenConnectPath: exe}
	if err := first.Connect(p, settings); err != nil {
		t.Fatal(err)
	}
	oldPID := first.Status().PID
	// A new manager recovers metadata written by the previous owner. Its fake
	// subprocess never created any real network configuration.
	second := NewManagerWithNetworkDir(dir)
	t.Cleanup(func() { _ = second.Disconnect() })
	p.ID = "new"
	if err := second.Connect(p, settings); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for first.Status().State != StateDisconnected && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if first.Status().State != StateDisconnected {
		t.Fatal("orphan was not stopped")
	}
	if second.Status().PID == oldPID || second.Status().PID == 0 {
		t.Fatal("replacement process missing")
	}
	identity, err := processIdentity(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	n := baseNetworkSession(dir, "aaaaaaaaaaaaaaaa")
	data, _ := json.Marshal(processRecord{PID: os.Getpid(), Identity: identity ^ 1})
	if err := os.WriteFile(n.state+".process", data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := n.recoverProcess(); err != nil {
		t.Fatal(err)
	}
}

func TestWindowsNetworkPlanAndLifecycle(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("PowerShell networking planner")
	}
	root, err := createNetworkDir()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	ps := filepath.Join(os.Getenv("SystemRoot"), "System32", "WindowsPowerShell", "v1.0", "powershell.exe")
	for _, full := range []bool{false, true} {
		cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", filepath.Join(root, "multi.ps1"), "-PlanOnly")
		cmd.Env = append(os.Environ(), "OCM_FULL="+map[bool]string{true: "true", false: "false"}[full], "OCM_ROUTES=[]", "OCM_DNS_DOMAINS=[]", "CISCO_SPLIT_INC=2", "CISCO_SPLIT_INC_0_ADDR=10.110.16.7", "CISCO_SPLIT_INC_0_MASKLEN=20", "CISCO_SPLIT_INC_1_ADDR=0.0.0.0", "CISCO_SPLIT_INC_1_MASKLEN=0", "INTERNAL_IP4_DNS=10.110.16.6", "CISCO_DEF_DOMAIN=corp.example.com", "CISCO_SPLIT_DNS=", "CISCO_SPLIT_EXC=0")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("plan: %v %s", err, out)
		}
		var routes []string
		var raw map[string]json.RawMessage
		if err := json.Unmarshal(out, &raw); err != nil {
			t.Fatalf("%s: %v", out, err)
		}
		_ = json.Unmarshal(raw["routes"], &routes)
		joined := strings.Join(routes, ",")
		if !strings.Contains(joined, "10.110.16.0/20") || !strings.Contains(joined, "10.110.16.6/32") {
			t.Fatal(string(out))
		}
		if strings.Contains(joined, "0.0.0.0/1") != full {
			t.Fatal("wrong default routing: " + string(out))
		}
	}
	// The harness replaces every Windows networking command with in-memory
	// mocks, and runs the actual journal, rollback and disconnect code.
	harness, err := filepath.Abs(filepath.Join("network", "multi_test.ps1"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(ps, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", harness, "-NetworkScript", filepath.Join(root, "multi.ps1"), "-StateRoot", root)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("lifecycle: %v\n%s", err, out)
	}
}

func TestNoFalseReadyTimer(t *testing.T) {
	t.Setenv("OCM_FAKE_VPN", "1")
	exe, _ := os.Executable()
	m := NewManager()
	t.Cleanup(func() { _ = m.Disconnect() })
	if err := m.Connect(vault.Profile{ID: "pending", Server: "test.invalid", Password: "test"}, vault.Settings{OpenConnectPath: exe}); err != nil {
		t.Fatal(err)
	}
	time.Sleep(1400 * time.Millisecond)
	if m.Status().State != StateConnecting {
		t.Fatal("running process was incorrectly reported as connected")
	}
}
