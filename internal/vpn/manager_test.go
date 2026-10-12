package vpn

import (
	"fmt"
	"io"
	"os"
	"sync"
	"testing"
	"time"

	"openconnectmulti/internal/vault"
)

// The subprocess accepts OpenConnect's arguments but never touches the network.
func TestMain(m *testing.M) {
	if os.Getenv("OCM_FAKE_VPN") == "1" {
		_, _ = io.Copy(io.Discard, os.Stdin)
		fmt.Println("fake VPN process ready")
		for {
			time.Sleep(time.Second)
		}
	}
	os.Exit(m.Run())
}

func TestConcurrentSwitchAndDisconnect(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OCM_FAKE_VPN", "1")
	manager := NewManager()
	t.Cleanup(func() { _ = manager.Disconnect() })
	var wg sync.WaitGroup
	for _, id := range []string{"first", "second"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := manager.Connect(vault.Profile{ID: id, Name: id, Server: "test.invalid", Password: "test"}, vault.Settings{OpenConnectPath: executable}); err != nil {
				t.Error(err)
			}
		}(id)
	}
	wg.Wait()
	starts, exits := 0, 0
	for _, entry := range manager.Logs() {
		if len(entry.Message) >= 9 && entry.Message[:9] == "starting " {
			starts++
		}
		if len(entry.Message) >= 18 && entry.Message[:18] == "openconnect exited" {
			exits++
		}
	}
	if starts != 2 || exits != 1 {
		t.Fatalf("switch must stop old process before starting new: starts=%d exits=%d", starts, exits)
	}
	if err := manager.Disconnect(); err != nil {
		t.Fatal(err)
	}
	if status := manager.Status(); status.State != StateDisconnected || status.PID != 0 {
		t.Fatalf("disconnect returned before cleanup: %+v", status)
	}
	if status := manager.Status(); status.LastError != "" {
		t.Fatalf("intentional disconnect reported as failure: %+v", status)
	}
}
