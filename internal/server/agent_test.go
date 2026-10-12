package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"openconnectmulti/internal/agentbridge"
	"openconnectmulti/internal/mcpserver"
	"openconnectmulti/internal/vault"
	"openconnectmulti/internal/vpn"
)

type fakeVPN struct {
	status                vpn.Status
	connects, disconnects int
}

func (f *fakeVPN) Status() vpn.Status   { return f.status }
func (f *fakeVPN) Logs() []vpn.LogEntry { return nil }
func (f *fakeVPN) Connect(p vault.Profile, _ vault.Settings) error {
	f.connects++
	f.status = vpn.Status{State: vpn.StateConnecting, ProfileID: p.ID, ProfileName: p.Name}
	return nil
}
func (f *fakeVPN) Disconnect() error {
	f.disconnects++
	f.status = vpn.Status{State: vpn.StateDisconnected}
	return nil
}

func TestAgentAuthorizationAndSelection(t *testing.T) {
	store, err := vault.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	manager := &fakeVPN{status: vpn.Status{State: vpn.StateDisconnected}}
	s := New(store, manager)
	token := s.EnableAgents()
	h := s.Handler()
	call := func(action, profile, auth, origin, remote string) *httptest.ResponseRecorder {
		body, _ := json.Marshal(agentbridge.Request{Action: action, Profile: profile})
		r := httptest.NewRequest(http.MethodPost, "http://127.0.0.1/api/agent", bytes.NewReader(body))
		r.RemoteAddr = remote
		r.Header.Set("Authorization", auth)
		r.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, tc := range []struct{ auth, origin, remote string }{
		{"", "", "127.0.0.1:10"}, {"Bearer wrong", "", "127.0.0.1:10"},
		{"Bearer " + token, "https://example.com", "127.0.0.1:10"}, {"Bearer " + token, "", "192.0.2.1:10"},
	} {
		if w := call("disconnect", "", tc.auth, tc.origin, tc.remote); w.Code != 403 {
			t.Fatalf("unauthorized: %d", w.Code)
		}
	}
	invoke := func(action, profile string) *httptest.ResponseRecorder {
		return call(action, profile, "Bearer "+token, "", "127.0.0.1:10")
	}
	if w := invoke("status", ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"unlocked":false`) {
		t.Fatal(w.Body.String())
	}
	for _, action := range []string{"profiles", "switch", "disconnect"} {
		if w := invoke(action, "office"); w.Code != 423 {
			t.Fatalf("locked %s: %d", action, w.Code)
		}
	}
	u, err := store.Initialize("1234")
	if err != nil {
		t.Fatal(err)
	}
	s.unlocked = u
	u.Data.Profiles = []vault.Profile{
		{ID: "office", Name: "Office", Server: "vpn.example.com", Password: "DO-NOT-EXPOSE", Username: "PRIVATE-USER", ExtraArgs: []string{"PRIVATE-ARG"}},
		{ID: "a", Name: "Duplicate"}, {ID: "b", Name: "duplicate"},
	}
	w := invoke("profiles", "")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	for _, secret := range []string{"DO-NOT-EXPOSE", "PRIVATE-USER", "PRIVATE-ARG", "password", "extra_args"} {
		if strings.Contains(w.Body.String(), secret) {
			t.Fatalf("leaked %s", secret)
		}
	}
	for _, query := range []string{"", "Off", "missing", "duplicate"} {
		if w := invoke("switch", query); w.Code != 400 {
			t.Fatalf("bad query %q: %d", query, w.Code)
		}
	}
	if manager.connects != 0 {
		t.Fatal("invalid selection changed VPN")
	}
	if w := invoke("switch", "OFFICE"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if w := invoke("switch", "office"); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if manager.connects != 1 {
		t.Fatal("same profile reconnected")
	}
	if s.agentLastAction == "" {
		t.Fatal("missing activity")
	}
	if w := invoke("switch", "a"); w.Code != 200 || manager.status.ProfileID != "a" {
		t.Fatal("exact ID failed")
	}
	if w := invoke("disconnect", ""); w.Code != 200 || manager.disconnects != 1 {
		t.Fatal("disconnect failed")
	}
	s.unlocked = nil
	if w := invoke("switch", "office"); w.Code != 423 {
		t.Fatal("lock did not revoke agent access")
	}
}

type testBridge struct{ dir string }

func (b testBridge) Call(ctx context.Context, r agentbridge.Request) (map[string]any, error) {
	return agentbridge.Call(ctx, b.dir, r)
}
func (b testBridge) Open(context.Context) (map[string]any, error) { return map[string]any{}, nil }

func TestMCPToDesktopIntegration(t *testing.T) {
	dir := t.TempDir()
	store, err := vault.NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	s := New(store, &fakeVPN{status: vpn.Status{State: vpn.StateDisconnected}})
	token := s.EnableAgents()
	httpServer := httptest.NewServer(s.Handler())
	defer httpServer.Close()
	cleanup, err := agentbridge.Publish(dir, agentbridge.Endpoint{URL: httpServer.URL, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	input := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"tools/list"}
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"vpn_status","arguments":{}}}
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"vpn_switch_profile","arguments":{"profile":"office"}}}
`
	var out bytes.Buffer
	if err := mcpserver.Serve(context.Background(), strings.NewReader(input), &out, testBridge{dir}); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("notification emitted response: %s", out.String())
	}
	if !strings.Contains(lines[1], "vpn_switch_profile") || !strings.Contains(lines[2], `\"running\":true`) || !strings.Contains(lines[3], `"isError":true`) {
		t.Fatal(out.String())
	}
}
