package server

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"time"

	"openconnectmulti/internal/agentbridge"
	"openconnectmulti/internal/vault"
	"openconnectmulti/internal/vpn"
)

// EnableAgents must be called before serving HTTP. The token is never sent to UI.
func (s *Server) EnableAgents() string {
	s.agentToken = randomToken()
	return s.agentToken
}

func (s *Server) handleAgent(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil || !net.ParseIP(host).IsLoopback() || r.Header.Get("Origin") != "" || s.agentToken == "" || subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), []byte("Bearer "+s.agentToken)) != 1 {
		writeError(w, http.StatusForbidden, "agent access denied")
		return
	}
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var input agentbridge.Request
	if !readJSON(w, r, &input) {
		return
	}
	if input.Action != "status" && input.Action != "profiles" && input.Action != "switch" && input.Action != "disconnect" {
		writeError(w, http.StatusBadRequest, "unknown agent action")
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if input.Action == "status" {
		writeJSON(w, http.StatusOK, s.agentStatusLocked())
		return
	}
	if s.unlocked == nil {
		writeError(w, http.StatusLocked, "Unlock OpenConnect Multi with your PIN in the application window, then retry. Never send the PIN to the agent.")
		return
	}
	if input.Action == "profiles" {
		profiles := make([]map[string]any, 0, len(s.unlocked.Data.Profiles))
		for _, p := range s.unlocked.Data.Profiles {
			profiles = append(profiles, map[string]any{"id": p.ID, "name": p.Name, "server": p.Server, "protocol": p.Protocol})
		}
		writeJSON(w, http.StatusOK, map[string]any{"profiles": profiles})
		return
	}
	if input.Action == "disconnect" {
		if err := s.vpn.Disconnect(); err != nil {
			writeError(w, http.StatusBadGateway, "Could not stop VPN; inspect the application journal")
			return
		}
		s.agentLastAction = "Агент отключил VPN"
		s.agentLastAt = time.Now()
		writeJSON(w, http.StatusOK, s.agentStatusLocked())
		return
	}
	profile, problem := resolveAgentProfile(s.unlocked.Data.Profiles, input.Profile)
	if problem != "" {
		writeError(w, http.StatusBadRequest, problem)
		return
	}
	current := s.vpn.Status()
	if current.ProfileID == profile.ID && (current.State == vpn.StateConnected || current.State == vpn.StateConnecting) {
		writeJSON(w, http.StatusOK, s.agentStatusLocked())
		return
	}
	if err := s.vpn.Connect(profile, s.unlocked.Data.Settings); err != nil {
		writeError(w, http.StatusBadGateway, "VPN could not start; inspect the application journal and profile settings")
		return
	}
	s.agentLastAction = "Агент выбрал VPN: " + profile.Name
	s.agentLastAt = time.Now()
	writeJSON(w, http.StatusOK, s.agentStatusLocked())
}

func resolveAgentProfile(profiles []vault.Profile, query string) (vault.Profile, string) {
	query = strings.TrimSpace(query)
	if query == "" {
		return vault.Profile{}, "profile is required; call vpn_list_profiles first"
	}
	for _, p := range profiles {
		if p.ID == query {
			return p, ""
		}
	}
	var matches []vault.Profile
	for _, p := range profiles {
		if strings.EqualFold(p.Name, query) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], ""
	}
	if len(matches) > 1 {
		return vault.Profile{}, "ambiguous profile name; use an exact ID from vpn_list_profiles"
	}
	return vault.Profile{}, "profile not found; use an exact name or ID from vpn_list_profiles"
}

func (s *Server) agentStatusLocked() map[string]any {
	status := s.vpn.Status()
	next := "Call vpn_list_profiles, then vpn_switch_profile with an exact profile ID or name."
	if s.unlocked == nil {
		next = "Unlock the app in its window. Do not ask for the PIN in chat."
	}
	return map[string]any{
		"running": true, "unlocked": s.unlocked != nil, "initialized": s.store.Initialized(),
		"state": status.State, "profile_id": status.ProfileID, "profile_name": status.ProfileName,
		"next_step":       next,
		"connection_note": "connected means the OpenConnect process is running; tunnel reachability is not verified",
	}
}
