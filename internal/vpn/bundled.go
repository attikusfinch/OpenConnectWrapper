package vpn

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func ResolveOpenConnectPath(configured string) string {
	configured = strings.TrimSpace(configured)
	if configured == "" || configured == "openconnect" {
		if bundled := BundledOpenConnectPath(); bundled != "" {
			return bundled
		}
		return "openconnect"
	}
	return configured
}

func BundledOpenConnectPath() string {
	if runtime.GOOS != "windows" {
		return ""
	}

	candidates := make([]string, 0, 6)
	if exe, err := os.Executable(); err == nil {
		base := filepath.Dir(exe)
		candidates = append(candidates,
			filepath.Join(base, "openconnect", "windows-amd64", "openconnect.exe"),
			filepath.Join(base, "third_party", "openconnect", "windows-amd64", "openconnect.exe"),
			filepath.Join(filepath.Dir(base), "third_party", "openconnect", "windows-amd64", "openconnect.exe"),
		)
	}
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates,
			filepath.Join(cwd, "openconnect", "windows-amd64", "openconnect.exe"),
			filepath.Join(cwd, "third_party", "openconnect", "windows-amd64", "openconnect.exe"),
		)
	}

	for _, candidate := range candidates {
		if candidate == "" {
			continue
		}
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			return candidate
		}
	}
	return ""
}
