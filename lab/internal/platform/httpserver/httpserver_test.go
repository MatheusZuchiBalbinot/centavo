package httpserver_test

import (
	"context"
	"net"
	"net/http"
	"testing"
	"time"

	"centavo/lab/internal/platform/httpserver"
)

const runReturnTimeout = 5 * time.Second

func TestRun_ContextCanceled_ShutsDownCleanly(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	server := httpserver.New("127.0.0.1:0", http.NewServeMux())
	runResult := make(chan error, 1)

	go func() {
		runResult <- httpserver.Run(ctx, server)
	}()

	cancel()

	err := waitForRun(t, runResult)
	if err != nil {
		t.Fatalf("Run returned %v, want nil", err)
	}
}

func TestRun_ListenFails_ReturnsError(t *testing.T) {
	var listenConfig net.ListenConfig

	occupied, err := listenConfig.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserving a port: %v", err)
	}
	defer occupied.Close()

	server := httpserver.New(occupied.Addr().String(), http.NewServeMux())
	runResult := make(chan error, 1)

	go func() {
		runResult <- httpserver.Run(context.Background(), server)
	}()

	err = waitForRun(t, runResult)
	if err == nil {
		t.Fatal("Run returned nil for an address already in use")
	}
}

func waitForRun(t *testing.T, runResult <-chan error) error {
	t.Helper()

	select {
	case err := <-runResult:
		return err
	case <-time.After(runReturnTimeout):
		t.Fatal("Run did not return in time")

		return nil
	}
}
