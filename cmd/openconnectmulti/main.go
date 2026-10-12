package main

import (
	"context"
	"flag"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"time"

	"openconnectmulti/internal/agentbridge"
	"openconnectmulti/internal/desktop"
	"openconnectmulti/internal/server"
	"openconnectmulti/internal/vault"
	"openconnectmulti/internal/vpn"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:49111", "HTTP listen address")
	configDir := flag.String("config-dir", "", "config directory for the encrypted vault")
	browser := flag.Bool("browser", false, "open in the default browser instead of a desktop window")
	serverOnly := flag.Bool("server-only", false, "run only the local HTTP server")
	noBrowser := flag.Bool("no-browser", false, "deprecated alias for --server-only")
	flag.Parse()

	store, err := vault.NewStore(*configDir)
	if err != nil {
		log.Fatalf("create vault store: %v", err)
	}
	closeLog := setupLogging(store.Dir())
	defer closeLog()
	// Reuse an existing instance instead of splitting UI and agent state.
	checkCtx, checkCancel := context.WithTimeout(context.Background(), 15*time.Second)
	endpoint, endpointErr := agentbridge.Read(store.Dir())
	if endpointErr == nil {
		if _, err := endpoint.Call(checkCtx, agentbridge.Request{Action: "status"}); err == nil {
			checkCancel()
			if !*serverOnly && !*noBrowser {
				_ = openBrowser(endpoint.URL)
			}
			return
		}
	}
	checkCancel()

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
	if tcp, ok := listener.Addr().(*net.TCPAddr); ok && tcp.IP.Equal(net.ParseIP("127.0.0.1")) {
		agentToken := app.EnableAgents()
		cleanup, err := agentbridge.Publish(store.Dir(), agentbridge.Endpoint{URL: url, Token: agentToken})
		if err != nil {
			log.Fatalf("publish agent endpoint: %v", err)
		}
		defer cleanup()
	}
	log.Printf("OpenConnect Multi is running: %s", url)
	log.Printf("Vault: %s", store.VaultPath())

	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	if *serverOnly || *noBrowser {
		waitForExit(errCh)
	} else if *browser {
		if err := openBrowser(url); err != nil {
			log.Printf("open browser: %v", err)
		}
		waitForExit(errCh)
	} else {
		if err := desktop.Open(url, store.Dir()); err != nil {
			log.Printf("desktop window: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = httpServer.Shutdown(ctx)
	_ = vpnManager.Disconnect()
}

func setupLogging(configDir string) func() {
	logPath := filepath.Join(configDir, "openconnectmulti.log")
	file, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		log.Printf("open log file: %v", err)
		return func() {}
	}
	log.SetOutput(io.MultiWriter(os.Stderr, file))
	return func() {
		_ = file.Close()
	}
}

func waitForExit(errCh <-chan error) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	select {
	case sig := <-sigCh:
		log.Printf("received %s, shutting down", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}
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
