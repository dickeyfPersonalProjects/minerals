package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// adminProbes is the subset of the API's probe surface that the admin
// listener mirrors. Liveness is process-only ("ok"); readiness reuses
// the same dependency-check logic the API's `/readyz` runs, so the
// kubelet sees the same answer whether it scrapes through the admin
// port or (historically) through the API port.
type adminProbes struct {
	readyz http.HandlerFunc
}

// newAdminHandler returns the http.Handler served on the admin port.
// It exposes:
//
//   - GET /metrics  — Prometheus exposition (default registry; ships
//     the `go_*` and `process_*` runtime collectors for free).
//   - GET /healthz  — liveness, plain-text "ok" (no dependency checks).
//   - GET /readyz   — readiness, delegated to the same handler the API
//     uses so the answer is identical.
//
// Anything else returns 404. The admin port is NEVER exposed to the
// public Ingress (defense-in-depth: even if a future ingress edit
// allows "all Service ports", nothing public-facing routes here —
// `kustomize/base/service.yaml` and the per-env ingresses both
// document this invariant).
func newAdminHandler(probes adminProbes) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", promhttp.Handler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok"))
	})
	if probes.readyz != nil {
		mux.HandleFunc("GET /readyz", probes.readyz)
	}
	return mux
}

// startAdminServer spawns the admin listener on its own goroutine and
// returns a shutdown function the main serve loop calls during
// graceful shutdown. Listener errors are reported on errCh; a closed
// channel signals a clean exit.
func startAdminServer(addr string, handler http.Handler) (shutdown func(context.Context) error, errCh <-chan error) {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	out := make(chan error, 1)
	go func() {
		slog.Info("admin listener starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			out <- err
		}
		close(out)
	}()
	return srv.Shutdown, out
}
