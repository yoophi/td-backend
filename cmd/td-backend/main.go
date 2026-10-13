package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/yoophi/td-backend/internal/adapter/httpapi"
	"github.com/yoophi/td-backend/internal/adapter/httpapi/receiptcodec"
	"github.com/yoophi/td-backend/internal/adapter/identity"
	"github.com/yoophi/td-backend/internal/adapter/sqlite"
	"github.com/yoophi/td-backend/internal/adapter/tokenauth"
	"github.com/yoophi/td-backend/internal/application/project"
	"github.com/yoophi/td-backend/internal/config"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func run(ctx context.Context, args []string) error {
	cfg, err := config.Load(args, os.Getenv)
	if err != nil {
		return err
	}
	if len(cfg.BootstrapToken) < 32 {
		return fmt.Errorf("TD_BACKEND_BOOTSTRAP_TOKEN must contain at least 32 characters")
	}
	store, err := sqlite.Open(ctx, cfg.DBPath, sqlite.Options{Readers: cfg.Readers, BusyTimeout: cfg.BusyTimeout})
	if err != nil {
		return err
	}
	defer store.Close()
	auth := tokenauth.New(store, store)
	if err = auth.Bootstrap(ctx, cfg.BootstrapToken); err != nil {
		return err
	}
	ids := identity.New()
	projects := &project.Service{Writes: store, Reads: store, Encoder: receiptcodec.Codec{}, ID: ids.ID, Now: time.Now}
	listener, err := net.Listen("tcp", cfg.Address)
	if err != nil {
		return err
	}
	server := &http.Server{Handler: httpapi.New(httpapi.Options{Projects: projects, Auth: auth}), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, IdleTimeout: time.Minute, BaseContext: func(net.Listener) context.Context { return ctx }}
	// WriteTimeout stays zero for future long-lived SSE streams.
	slog.Info("td-backend listening", "address", listener.Addr().String(), "sqlite", store.Version())
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	select {
	case err = <-done:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		stop, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err = server.Shutdown(stop); err != nil {
			_ = server.Close()
			return err
		}
		err = <-done
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "td-backend:", err)
		os.Exit(1)
	}
}
