package httpserver

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"time"
)

const (
	readHeaderTimeout = 2 * time.Second
	writeTimeout      = 10 * time.Second
	shutdownTimeout   = 10 * time.Second
)

func New(address string, handler http.Handler) *http.Server {
	return &http.Server{
		Addr: address, Handler: handler, ReadHeaderTimeout: readHeaderTimeout, WriteTimeout: writeTimeout,
	}
}

// In-flight requests get up to shutdownTimeout to finish.
func Run(ctx context.Context, servers ...*http.Server) error {
	serveErrors := make(chan error, len(servers))

	for _, server := range servers {
		go func() {
			serveErrors <- listen(server)
		}()
	}

	var runErr error

	select {
	case <-ctx.Done():
	case runErr = <-serveErrors:
	}

	shutdownErr := shutdownAll(servers)

	return errors.Join(runErr, shutdownErr)
}

func listen(server *http.Server) error {
	err := server.ListenAndServe()

	isGracefulStop := errors.Is(err, http.ErrServerClosed)
	if isGracefulStop {
		return nil
	}

	return fmt.Errorf("serving %s: %w", server.Addr, err)
}

func shutdownAll(servers []*http.Server) error {
	// The parent context is already canceled at this point, so shutdown gets
	// a fresh deadline of its own.
	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()

	var shutdownErrors []error

	for _, server := range servers {
		err := server.Shutdown(ctx)
		if err != nil {
			shutdownErrors = append(shutdownErrors, fmt.Errorf("shutting down %s: %w", server.Addr, err))
		}
	}

	return errors.Join(shutdownErrors...)
}
