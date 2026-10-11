package main

import (
	"context"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"testing"
	"time"
)

func testEnv(overrides map[string]string) func(string) string {
	env := map[string]string{
		"API_KEY":      "test-key",
		"LISTEN_ADDR":  "127.0.0.1:0",
		"DATABASE_URL": "postgres://user:pass@127.0.0.1:1/db?sslmode=disable",
	}
	for k, v := range overrides {
		env[k] = v
	}
	return func(k string) string { return env[k] }
}

func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

func TestRun_ServesHealthzAndShutsDownOnCancel(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- run(ctx, testEnv(nil), discardLogger(), ready) }()

	var addr net.Addr
	select {
	case addr = <-ready:
	case err := <-done:
		t.Fatalf("run exited before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr.String()+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /healthz status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down")
	}
}

func TestRun_StartupErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr string
	}{
		{name: "invalid config", env: map[string]string{"API_KEY": ""}, wantErr: "API_KEY is required"},
		{name: "invalid database url", env: map[string]string{"DATABASE_URL": "::not a url::"}, wantErr: "database pool"},
		{name: "invalid listen address", env: map[string]string{"LISTEN_ADDR": "not-an-address"}, wantErr: "listen"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := run(t.Context(), testEnv(tt.env), discardLogger(), nil)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("run() error = %v, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// Not parallel: it sends SIGTERM to the test process itself. NotifyContext
// intercepts the signal, so the process survives and run drains and returns.
func TestRunUntilSignal_GracefulShutdownOnSIGTERM(t *testing.T) {
	ready := make(chan net.Addr, 1)
	done := make(chan error, 1)
	go func() { done <- runUntilSignal(testEnv(nil), discardLogger(), ready) }()

	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("runUntilSignal exited before listening: %v", err)
	case <-time.After(5 * time.Second):
		t.Fatal("server did not start")
	}

	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("runUntilSignal returned %v after SIGTERM, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not shut down on SIGTERM")
	}
}
