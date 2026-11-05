package server

import (
	"net/http"
	"openconnectmulti/internal/vault"
	"sort"
)

// Resolve and validate the whole selection before starting any process. Runtime
// failures are reported per profile; already working tunnels stay connected.
func (s *Server) handleConnectSelected(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req struct {
		Profiles []string `json:"profiles"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	if len(req.Profiles) == 0 || len(req.Profiles) > 100 {
		writeError(w, http.StatusBadRequest, "Выберите от 1 до 100 профилей")
		return
	}
	s.mu.Lock()
	if s.unlocked == nil {
		s.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "locked")
		return
	}
	settings := s.unlocked.Data.Settings
	if !settings.MultiMode {
		s.mu.Unlock()
		writeError(w, http.StatusConflict, "Сначала включите режим Мульти")
		return
	}
	profiles := []vault.Profile{}
	seen := map[string]bool{}
	for _, id := range req.Profiles {
		idx := findProfileIndex(s.unlocked.Data.Profiles, id)
		if idx < 0 {
			s.mu.Unlock()
			writeError(w, http.StatusNotFound, "profile not found")
			return
		}
		if !seen[id] {
			profiles = append(profiles, s.unlocked.Data.Profiles[idx])
			seen[id] = true
		}
	}
	defer s.mu.Unlock()
	// Configure work tunnels before the default-route owner, reducing transport
	// changes during their initial authentication and honoring their exclusions.
	sort.SliceStable(profiles, func(i, j int) bool {
		return profiles[i].ID != settings.InternetProfile && profiles[j].ID == settings.InternetProfile
	})
	problems := map[string]string{}
	for _, p := range profiles {
		if err := s.vpn.Connect(p, settings); err != nil {
			problems[p.ID] = err.Error()
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": len(problems) == 0, "errors": problems, "connections": s.vpn.Statuses()})
}
