package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"openconnectmulti/internal/vault"
	"openconnectmulti/internal/vpn"
	"strings"
	"testing"
)

type multiFake struct {
	statuses map[string]vpn.Status
	starts   int
}

func (f *multiFake) Status() vpn.Status {
	for _, s := range f.statuses {
		if s.State != vpn.StateDisconnected {
			return s
		}
	}
	return vpn.Status{State: vpn.StateDisconnected}
}
func (f *multiFake) Statuses() []vpn.Status {
	out := []vpn.Status{}
	for _, s := range f.statuses {
		out = append(out, s)
	}
	return out
}
func (f *multiFake) Logs() []vpn.LogEntry { return nil }
func (f *multiFake) Connect(p vault.Profile, s vault.Settings) error {
	if !s.MultiMode {
		_ = f.Disconnect()
	}
	f.starts++
	f.statuses[p.ID] = vpn.Status{State: vpn.StateConnecting, ProfileID: p.ID, ProfileName: p.Name, Command: "PRIVATE-AUTH-ARG"}
	return nil
}
func (f *multiFake) Disconnect() error {
	for id := range f.statuses {
		_ = f.DisconnectProfile(id)
	}
	return nil
}
func (f *multiFake) DisconnectProfile(id string) error {
	if s, ok := f.statuses[id]; ok {
		s.State = vpn.StateDisconnected
		f.statuses[id] = s
	}
	return nil
}

func TestMultiAPISelectionAndScope(t *testing.T) {
	store, err := vault.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	u, err := store.Initialize("1234")
	if err != nil {
		t.Fatal(err)
	}
	u.Data.Settings = vault.Settings{OpenConnectPath: "openconnect", MultiMode: true, InternetProfile: "a"}
	u.Data.Profiles = []vault.Profile{{ID: "a", Name: "A"}, {ID: "b", Name: "B"}}
	f := &multiFake{statuses: map[string]vpn.Status{}}
	s := New(store, f)
	s.unlocked = u
	s.sessionToken = "test-session"
	s.EnableAgents()
	h := s.Handler()
	call := func(path string, payload any, authorized bool) *httptest.ResponseRecorder {
		b, _ := json.Marshal(payload)
		r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(b))
		if authorized {
			r.AddCookie(&http.Cookie{Name: "ocm_session", Value: "test-session"})
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := call("/api/connect-selected", map[string]any{"profiles": []string{"a"}}, false); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if w := call("/api/connect-selected", map[string]any{"profiles": []string{"a", "missing"}}, true); w.Code != 404 || f.starts != 0 {
		t.Fatal("partial invalid selection changed VPN")
	}
	if w := call("/api/connect-selected", map[string]any{"profiles": []string{"a", "a", "b"}}, true); w.Code != 200 || f.starts != 2 {
		t.Fatal(w.Body.String())
	}
	if w := call("/api/disconnect/a", nil, true); w.Code != 200 || f.statuses["b"].State != vpn.StateConnecting {
		t.Fatal("individual disconnect stopped unrelated tunnel")
	}
	if w := call("/api/settings", vault.Settings{OpenConnectPath: "openconnect", MultiMode: false}, true); w.Code != 409 {
		t.Fatal("allowed mode change while connected")
	}
	if w := call("/api/disconnect", nil, true); w.Code != 200 {
		t.Fatal(w.Code)
	}
	if w := call("/api/settings", vault.Settings{OpenConnectPath: "openconnect", MultiMode: false}, true); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	// The MCP status must never expose command lines or authentication arguments.
	b, _ := json.Marshal(map[string]any{"action": "status"})
	r := httptest.NewRequest(http.MethodPost, "/api/agent", bytes.NewReader(b))
	r.RemoteAddr = "127.0.0.1:10"
	r.Header.Set("Authorization", "Bearer "+s.agentToken)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || strings.Contains(w.Body.String(), "PRIVATE-AUTH-ARG") {
		t.Fatal("MCP status disclosed arguments: " + w.Body.String())
	}
}
