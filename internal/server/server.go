package server

import (
	"crypto/rand"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"openconnectmulti/internal/vault"
	"openconnectmulti/internal/vpn"
)

//go:embed static/*
var staticFiles embed.FS

var pinPattern = regexp.MustCompile(`^\d{4}$`)

type Server struct {
	store *vault.Store
	vpn   *vpn.Manager

	mu             sync.Mutex
	unlocked       *vault.Unlocked
	sessionToken   string
	failedUnlocks  int
	unlockLockedTo time.Time
}

func New(store *vault.Store, vpnManager *vpn.Manager) *Server {
	return &Server{store: store, vpn: vpnManager}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/static/", s.handleStatic)
	mux.HandleFunc("/api/status", s.handleStatus)
	mux.HandleFunc("/api/setup", s.handleSetup)
	mux.HandleFunc("/api/unlock", s.handleUnlock)
	mux.HandleFunc("/api/lock", s.handleLock)
	mux.HandleFunc("/api/profiles", s.requireAuth(s.handleProfiles))
	mux.HandleFunc("/api/profiles/", s.requireAuth(s.handleProfileByID))
	mux.HandleFunc("/api/connect/", s.requireAuth(s.handleConnect))
	mux.HandleFunc("/api/disconnect", s.requireAuth(s.handleDisconnect))
	mux.HandleFunc("/api/settings", s.requireAuth(s.handleSettings))
	mux.HandleFunc("/api/logs", s.requireAuth(s.handleLogs))
	return mux
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	data, err := staticFiles.ReadFile("static/index.html")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	http.FileServer(http.FS(staticFiles)).ServeHTTP(w, r)
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	unlocked := s.unlocked != nil && s.validSessionLocked(r)
	configuredOpenConnectPath := "openconnect"
	if s.unlocked != nil && s.unlocked.Data.Settings.OpenConnectPath != "" {
		configuredOpenConnectPath = s.unlocked.Data.Settings.OpenConnectPath
	}
	openConnectPath := vpn.ResolveOpenConnectPath(configuredOpenConnectPath)
	lockedUntil := s.unlockLockedTo
	s.mu.Unlock()

	_, lookupErr := exec.LookPath(openConnectPath)
	writeJSON(w, http.StatusOK, map[string]any{
		"initialized":                s.store.Initialized(),
		"unlocked":                   unlocked,
		"vault_path":                 s.store.VaultPath(),
		"config_dir":                 s.store.Dir(),
		"device_secret_path":         s.store.DeviceSecretPath(),
		"vpn":                        s.vpn.Status(),
		"openconnect_path":           configuredOpenConnectPath,
		"effective_openconnect_path": openConnectPath,
		"bundled_openconnect_path":   vpn.BundledOpenConnectPath(),
		"openconnect_found":          lookupErr == nil,
		"known_gui_path":             knownOpenConnectGUIPath(),
		"unlock_locked_to":           lockedUntil,
		"server_time":                time.Now(),
	})
}

func (s *Server) handleSetup(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req pinRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !pinPattern.MatchString(req.PIN) {
		writeError(w, http.StatusBadRequest, "PIN must contain exactly 4 digits")
		return
	}

	unlocked, err := s.store.Initialize(req.PIN)
	if errors.Is(err, vault.ErrAlreadyInitialized) {
		writeError(w, http.StatusConflict, "vault is already initialized")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	s.mu.Lock()
	s.unlocked = unlocked
	s.sessionToken = randomToken()
	s.failedUnlocks = 0
	s.unlockLockedTo = time.Time{}
	token := s.sessionToken
	s.mu.Unlock()

	setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	var req pinRequest
	if !readJSON(w, r, &req) {
		return
	}
	if !pinPattern.MatchString(req.PIN) {
		writeError(w, http.StatusBadRequest, "PIN must contain exactly 4 digits")
		return
	}

	s.mu.Lock()
	if time.Now().Before(s.unlockLockedTo) {
		lockedTo := s.unlockLockedTo
		s.mu.Unlock()
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"error":            "too many attempts",
			"unlock_locked_to": lockedTo,
		})
		return
	}
	s.mu.Unlock()

	unlocked, err := s.store.Unlock(req.PIN)
	if err != nil {
		s.mu.Lock()
		s.failedUnlocks++
		if s.failedUnlocks >= 5 {
			s.unlockLockedTo = time.Now().Add(30 * time.Second)
		}
		lockedTo := s.unlockLockedTo
		s.mu.Unlock()
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"error":            "wrong PIN",
			"failed_attempts":  s.failedUnlocks,
			"unlock_locked_to": lockedTo,
		})
		return
	}

	s.mu.Lock()
	s.unlocked = unlocked
	s.sessionToken = randomToken()
	s.failedUnlocks = 0
	s.unlockLockedTo = time.Time{}
	token := s.sessionToken
	s.mu.Unlock()

	setSessionCookie(w, token)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	s.mu.Lock()
	s.unlocked = nil
	s.sessionToken = ""
	s.mu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "ocm_session",
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleProfiles(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.unlocked == nil {
			writeError(w, http.StatusUnauthorized, "locked")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profiles": profilesDTO(s.unlocked.Data.Profiles)})
	case http.MethodPost:
		var req profileRequest
		if !readJSON(w, r, &req) {
			return
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.unlocked == nil {
			writeError(w, http.StatusUnauthorized, "locked")
			return
		}
		profile, err := normalizeProfile(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		if profile.ID == "" {
			profile.ID = "p_" + randomToken()[:16]
			s.unlocked.Data.Profiles = append(s.unlocked.Data.Profiles, profile)
		} else {
			idx := findProfileIndex(s.unlocked.Data.Profiles, profile.ID)
			if idx < 0 {
				writeError(w, http.StatusNotFound, "profile not found")
				return
			}
			if profile.Password == "" {
				profile.Password = s.unlocked.Data.Profiles[idx].Password
			}
			profile.LastConnectedAt = s.unlocked.Data.Profiles[idx].LastConnectedAt
			profile.LastConnectedError = s.unlocked.Data.Profiles[idx].LastConnectedError
			s.unlocked.Data.Profiles[idx] = profile
		}
		if err := s.unlocked.Save(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": toProfileDTO(profile)})
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleProfileByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/profiles/")
	if id == "" {
		http.NotFound(w, r)
		return
	}
	if r.Method != http.MethodDelete {
		methodNotAllowed(w)
		return
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unlocked == nil {
		writeError(w, http.StatusUnauthorized, "locked")
		return
	}
	idx := findProfileIndex(s.unlocked.Data.Profiles, id)
	if idx < 0 {
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	s.unlocked.Data.Profiles = append(s.unlocked.Data.Profiles[:idx], s.unlocked.Data.Profiles[idx+1:]...)
	if err := s.unlocked.Save(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if s.vpn.Status().ProfileID == id {
		go func() { _ = s.vpn.Disconnect() }()
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/api/connect/")
	if id == "" {
		http.NotFound(w, r)
		return
	}

	s.mu.Lock()
	if s.unlocked == nil {
		s.mu.Unlock()
		writeError(w, http.StatusUnauthorized, "locked")
		return
	}
	idx := findProfileIndex(s.unlocked.Data.Profiles, id)
	if idx < 0 {
		s.mu.Unlock()
		writeError(w, http.StatusNotFound, "profile not found")
		return
	}
	profile := s.unlocked.Data.Profiles[idx]
	settings := s.unlocked.Data.Settings
	s.mu.Unlock()

	err := s.vpn.Connect(profile, settings)

	s.mu.Lock()
	if s.unlocked != nil {
		idx = findProfileIndex(s.unlocked.Data.Profiles, id)
		if idx >= 0 {
			s.unlocked.Data.Profiles[idx].LastConnectedAt = time.Now()
			s.unlocked.Data.Profiles[idx].LastConnectedError = ""
			if err != nil {
				s.unlocked.Data.Profiles[idx].LastConnectedError = err.Error()
			}
			_ = s.unlocked.Save()
		}
	}
	s.mu.Unlock()

	if err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vpn": s.vpn.Status()})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		methodNotAllowed(w)
		return
	}
	if err := s.vpn.Disconnect(); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "vpn": s.vpn.Status()})
}

func (s *Server) handleSettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.mu.Lock()
		defer s.mu.Unlock()
		if s.unlocked == nil {
			writeError(w, http.StatusUnauthorized, "locked")
			return
		}
		writeJSON(w, http.StatusOK, s.unlocked.Data.Settings)
	case http.MethodPost:
		var req vault.Settings
		if !readJSON(w, r, &req) {
			return
		}
		req.OpenConnectPath = strings.TrimSpace(req.OpenConnectPath)
		if req.OpenConnectPath == "" {
			req.OpenConnectPath = "openconnect"
		}

		s.mu.Lock()
		defer s.mu.Unlock()
		if s.unlocked == nil {
			writeError(w, http.StatusUnauthorized, "locked")
			return
		}
		s.unlocked.Data.Settings = req
		if err := s.unlocked.Save(); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, req)
	default:
		methodNotAllowed(w)
	}
}

func (s *Server) handleLogs(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		methodNotAllowed(w)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"logs": s.vpn.Logs()})
}

func (s *Server) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		ok := s.unlocked != nil && s.validSessionLocked(r)
		s.mu.Unlock()
		if !ok {
			writeError(w, http.StatusUnauthorized, "locked")
			return
		}
		next(w, r)
	}
}

func (s *Server) validSessionLocked(r *http.Request) bool {
	if s.sessionToken == "" {
		return false
	}
	cookie, err := r.Cookie("ocm_session")
	if err != nil {
		return false
	}
	return cookie.Value == s.sessionToken
}

type pinRequest struct {
	PIN string `json:"pin"`
}

type profileRequest struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Server      string   `json:"server"`
	Username    string   `json:"username"`
	Password    string   `json:"password"`
	AuthGroup   string   `json:"auth_group"`
	Protocol    string   `json:"protocol"`
	UserAgent   string   `json:"user_agent"`
	ServerCert  string   `json:"server_cert"`
	NoCertCheck bool     `json:"no_cert_check"`
	ExtraArgs   []string `json:"extra_args"`
}

type profileResponse struct {
	ID                 string    `json:"id"`
	Name               string    `json:"name"`
	Server             string    `json:"server"`
	Username           string    `json:"username"`
	AuthGroup          string    `json:"auth_group"`
	Protocol           string    `json:"protocol"`
	UserAgent          string    `json:"user_agent"`
	ServerCert         string    `json:"server_cert"`
	NoCertCheck        bool      `json:"no_cert_check"`
	ExtraArgs          []string  `json:"extra_args"`
	HasPassword        bool      `json:"has_password"`
	LastConnectedAt    time.Time `json:"last_connected_at,omitempty"`
	LastConnectedError string    `json:"last_connected_error,omitempty"`
}

func normalizeProfile(req profileRequest) (vault.Profile, error) {
	profile := vault.Profile{
		ID:          strings.TrimSpace(req.ID),
		Name:        strings.TrimSpace(req.Name),
		Server:      strings.TrimSpace(req.Server),
		Username:    strings.TrimSpace(req.Username),
		Password:    req.Password,
		AuthGroup:   strings.TrimSpace(req.AuthGroup),
		Protocol:    strings.TrimSpace(req.Protocol),
		UserAgent:   strings.TrimSpace(req.UserAgent),
		ServerCert:  strings.TrimSpace(req.ServerCert),
		NoCertCheck: req.NoCertCheck,
		ExtraArgs:   compactArgs(req.ExtraArgs),
	}
	if profile.Server == "" {
		return profile, errors.New("server is required")
	}
	if profile.Name == "" {
		profile.Name = profile.Server
	}
	return profile, nil
}

func compactArgs(args []string) []string {
	out := make([]string, 0, len(args))
	for _, arg := range args {
		arg = strings.TrimSpace(arg)
		if arg != "" {
			out = append(out, arg)
		}
	}
	return out
}

func findProfileIndex(profiles []vault.Profile, id string) int {
	for i, profile := range profiles {
		if profile.ID == id {
			return i
		}
	}
	return -1
}

func profilesDTO(profiles []vault.Profile) []profileResponse {
	out := make([]profileResponse, len(profiles))
	for i, profile := range profiles {
		out[i] = toProfileDTO(profile)
	}
	return out
}

func toProfileDTO(profile vault.Profile) profileResponse {
	return profileResponse{
		ID:                 profile.ID,
		Name:               profile.Name,
		Server:             profile.Server,
		Username:           profile.Username,
		AuthGroup:          profile.AuthGroup,
		Protocol:           profile.Protocol,
		UserAgent:          profile.UserAgent,
		ServerCert:         profile.ServerCert,
		NoCertCheck:        profile.NoCertCheck,
		ExtraArgs:          profile.ExtraArgs,
		HasPassword:        profile.Password != "",
		LastConnectedAt:    profile.LastConnectedAt,
		LastConnectedError: profile.LastConnectedError,
	}
}

func readJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	defer func() { _, _ = io.Copy(io.Discard, r.Body) }()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]any{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	writeError(w, http.StatusMethodNotAllowed, "method not allowed")
}

func setSessionCookie(w http.ResponseWriter, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     "ocm_session",
		Value:    token,
		Path:     "/",
		MaxAge:   12 * 60 * 60,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})
}

func randomToken() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		panic(fmt.Errorf("random token: %w", err))
	}
	return base64.RawURLEncoding.EncodeToString(buf)
}

func knownOpenConnectGUIPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "OpenConnect-GUI", "openconnect-gui.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "OpenConnect-GUI", "openconnect-gui.exe"),
	}
	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}
