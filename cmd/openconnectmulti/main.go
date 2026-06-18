package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"time"

	"openconnectmulti/internal/server"
	"openconnectmulti/internal/vault"
	"openconnectmulti/internal/vpn"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:49111", "HTTP listen address")
	configDir := flag.String("config-dir", "", "config directory for the encrypted vault")
	noBrowser := flag.Bool("no-browser", false, "do not open the browser automatically")
	flag.Parse()

	store, err := vault.NewStore(*configDir)
	if err != nil {
		log.Fatalf("create vault store: %v", err)
	}

	vpnManager := vpn.NewManager()
	app := server.New(store, vpnManager)
	httpServer := &http.Server{
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	listener, err := listen(*addr)
	if err != nil {
		log.Fatalf("listen: %v", err)
	}

	url := "http://" + listener.Addr().String()
	fmt.Printf("OpenConnect Multi is running: %s\n", url)
	fmt.Printf("Vault: %s\n", store.VaultPath())
	if !*noBrowser {
		_ = openBrowser(url)
	}

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)

	select {
	case sig := <-sigCh:
		fmt.Printf("\nreceived %s, shutting down...\n", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	_ = vpnManager.Disconnect()
}

func listen(addr string) (net.Listener, error) {
	listener, err := net.Listen("tcp", addr)
	if err == nil {
		return listener, nil
	}
	if addr == "127.0.0.1:49111" {
		return net.Listen("tcp", "127.0.0.1:0")
	}
	return nil, err
}

func openBrowser(url string) error {
	switch runtime.GOOS {
	case "windows":
		return exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		return exec.Command("open", url).Start()
	default:
		return exec.Command("xdg-open", url).Start()
	}
}
