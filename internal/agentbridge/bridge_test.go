package agentbridge

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiscoveryRotationAndRedirect(t *testing.T) {
	dir := t.TempDir()
	token := strings.Repeat("a", 43)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			t.Error("bad authentication")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	first, err := Publish(dir, Endpoint{URL: server.URL, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Call(context.Background(), dir, Request{Action: "status"}); err != nil {
		t.Fatal(err)
	}
	token = strings.Repeat("b", 43)
	second, err := Publish(dir, Endpoint{URL: server.URL, Token: token})
	if err != nil {
		t.Fatal(err)
	}
	first()
	if _, err := Call(context.Background(), dir, Request{Action: "status"}); err != nil {
		t.Fatal("stale cleanup removed new endpoint", err)
	}
	second()
	if _, err := Read(dir); err == nil {
		t.Fatal("cleanup failed")
	}
	leaked := false
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer target.Close()
	redirect := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, 307) }))
	defer redirect.Close()
	_, _ = (Endpoint{URL: redirect.URL, Token: token}).Call(context.Background(), Request{Action: "status"})
	if leaked {
		t.Fatal("token followed redirect")
	}
}

func TestRejectUnsafeDiscovery(t *testing.T) {
	dir := t.TempDir()
	for _, url := range []string{"https://example.com", "http://192.168.1.2:80", "http://127.0.0.1:80/extra", "http://user@127.0.0.1:80", "http://127.0.0.1:80?token=x"} {
		b, _ := json.Marshal(Endpoint{URL: url, Token: strings.Repeat("x", 43)})
		if err := os.WriteFile(filepath.Join(dir, DiscoveryFile), b, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Read(dir); err == nil {
			t.Fatalf("accepted %s", url)
		}
	}
}
