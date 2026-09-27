// Package httpapi is the driving adapter exposing simulation control over HTTP.
package httpapi

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/mymdz/lumo-market/internal/ports"
)

type Server struct {
	ctl   ports.Control
	store ports.Store
	srv   *http.Server
	log   *slog.Logger
}

func New(addr string, ctl ports.Control, store ports.Store, log *slog.Logger) *Server {
	s := &Server{ctl: ctl, store: store, log: log}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.help)
	mux.HandleFunc("GET /status", s.status)
	mux.HandleFunc("GET /stats", s.stats)
	mux.HandleFunc("POST /speed", s.speed)
	mux.HandleFunc("POST /pause", s.simple(func() { ctl.Pause() }))
	mux.HandleFunc("POST /resume", s.simple(func() { ctl.Resume() }))
	mux.HandleFunc("POST /resync", s.simple(func() { ctl.Resync() }))
	s.srv = &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	return s
}

func (s *Server) Start() {
	go func() {
		if err := s.srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			s.log.Error("http server", "err", err)
		}
	}()
}

func (s *Server) Shutdown(ctx context.Context) error { return s.srv.Shutdown(ctx) }

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func (s *Server) help(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, 200, map[string]any{
		"endpoints": map[string]string{
			"GET /status":      "simulation clock, phase, activity and active events",
			"GET /stats":       "approximate row counts per table",
			"POST /speed?x=10": "run simulated time 10x faster (needs allow_future) or slower (x=0.5)",
			"POST /pause":      "freeze simulated time",
			"POST /resume":     "continue after pause",
			"POST /resync":     "speed 1 and realign simulated time with wall time",
		},
	})
}

func (s *Server) status(w http.ResponseWriter, _ *http.Request) { writeJSON(w, 200, s.ctl.Status()) }

func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	st, err := s.store.Stats(r.Context())
	if err != nil {
		writeJSON(w, 500, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) speed(w http.ResponseWriter, r *http.Request) {
	x, err := strconv.ParseFloat(r.URL.Query().Get("x"), 64)
	if err != nil || x <= 0 || x > 100000 {
		writeJSON(w, 400, map[string]string{"error": "x must be a positive number, e.g. /speed?x=60"})
		return
	}
	if err := s.ctl.SetSpeed(x); err != nil {
		writeJSON(w, 409, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, 200, s.ctl.Status())
}

func (s *Server) simple(f func()) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		f()
		writeJSON(w, 200, s.ctl.Status())
	}
}
