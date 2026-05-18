package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/secrets"
	"github.com/sistemica/pantograf/state"
	"github.com/sistemica/pantograf/storage"
)

// cmdServe runs pgf as a long-lived HTTP daemon exposing the same action
// surface as the CLI. Callers POST JSON params to
// /v1/run/<type>/<name>/<action> and get the action result back — same
// shape `pgf run …` prints.
//
// Why: one fork+exec per syscall is ~10-20ms on Linux. Fine for shell
// agents at LLM speeds; meaningful for hot loops, multi-agent hosts, web
// frontends. The daemon caches Open()'d sessions so e.g. one matrix
// session token, one IMAP login, or one s3 client is reused across calls.
//
// Auth: --auth-token (or PGF_AUTH_TOKEN env) gates every endpoint via
// `Authorization: Bearer <token>`. Empty = no auth; default bind is
// loopback so unauth'd mode stays safe by default.
//
//   pgf serve                                 # 127.0.0.1:8765, no auth
//   pgf serve --addr :8765 --auth-token tok   # all interfaces, gated
//   PGF_AUTH_TOKEN=tok pgf serve --addr 0.0.0.0:8765
//
// Distinct from `pgf webhooks`: serve is the *outbound* RPC daemon
// (callers ask pgf to run actions for them). `webhooks` is the *inbound*
// receiver (the network pushes events into pgf).
func cmdServe(ctx context.Context, store storage.Store, vault *secrets.Vault, stateMgr state.Manager, args []string) {
	fs := flag.NewFlagSet("serve", flag.ContinueOnError)
	addr := fs.String("addr", "127.0.0.1:8765", "Listen address (host:port)")
	authToken := fs.String("auth-token", "", "Require Authorization: Bearer <token>. Empty = no auth (loopback default).")
	shutdownTimeout := fs.Duration("shutdown-timeout", 30*time.Second, "Max wait for in-flight requests on SIGINT/SIGTERM")
	if err := fs.Parse(args); err != nil {
		fail(err)
	}
	if *authToken == "" {
		if t := os.Getenv("PGF_AUTH_TOKEN"); t != "" {
			*authToken = t
		}
	}

	srv := &rpcServer{
		store:     store,
		vault:     vault,
		stateMgr:  stateMgr,
		authToken: *authToken,
		sessions:  map[string]connector.Session{},
	}

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           srv.mux(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	sigCtx, cancel := signal.NotifyContext(ctx, syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	serveErrCh := make(chan error, 1)
	go func() {
		log.Printf("pgf serve: listening on %s (auth=%t)", *addr, *authToken != "")
		serveErrCh <- httpSrv.ListenAndServe()
	}()

	select {
	case err := <-serveErrCh:
		if !errors.Is(err, http.ErrServerClosed) {
			fail(err)
		}
	case <-sigCtx.Done():
		log.Println("pgf serve: shutdown signal received")
	}

	shutdownCtx, sc := context.WithTimeout(context.Background(), *shutdownTimeout)
	defer sc()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Printf("pgf serve: shutdown error: %v", err)
	}
	srv.closeAll()
	log.Println("pgf serve: bye")
}

// rpcServer holds the long-lived state for the HTTP daemon. The name
// disambiguates from `webhooks.go`'s server struct (different concept).
type rpcServer struct {
	store     storage.Store
	vault     *secrets.Vault
	stateMgr  state.Manager
	authToken string

	mu       sync.Mutex
	sessions map[string]connector.Session // keyed by "type/name"
}

func (s *rpcServer) mux() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /v1/connectors", s.handleConnectors)
	mux.HandleFunc("GET /v1/connectors/{type}", s.handleConnector)
	mux.HandleFunc("GET /v1/instances", s.handleInstances)
	mux.HandleFunc("POST /v1/run/{type}/{name}/{action}", s.handleRun)
	return s.withAuth(mux)
}

// withAuth wraps mux with constant-time bearer-token comparison when a
// token is configured. /v1/health is also gated — random callers don't
// get to fingerprint the daemon.
func (s *rpcServer) withAuth(h http.Handler) http.Handler {
	if s.authToken == "" {
		return h
	}
	expected := []byte("Bearer " + s.authToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if len(got) != len(expected) || subtle.ConstantTimeCompare(got, expected) != 1 {
			writeRPCError(w, http.StatusUnauthorized, errors.New("unauthorized"))
			return
		}
		h.ServeHTTP(w, r)
	})
}

// closeAll closes every cached session. Called on shutdown so HTTP
// keep-alives, IMAP connections, etc. release cleanly.
func (s *rpcServer) closeAll() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, sess := range s.sessions {
		if err := sess.Close(); err != nil {
			log.Printf("pgf serve: close %s: %v", k, err)
		}
		delete(s.sessions, k)
	}
}

// ── handlers ──────────────────────────────────────────────────────────────

func (s *rpcServer) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeRPCJSON(w, http.StatusOK, map[string]any{
		"status":     "ok",
		"connectors": len(connector.Default.List()),
	})
}

func (s *rpcServer) handleConnectors(w http.ResponseWriter, _ *http.Request) {
	out := []any{}
	for _, d := range connector.Default.List() {
		out = append(out, map[string]any{
			"name":         d.Name,
			"display_name": d.DisplayName,
			"description":  d.Description,
			"version":      d.Version,
			"categories":   d.Categories,
		})
	}
	writeRPCJSON(w, http.StatusOK, out)
}

func (s *rpcServer) handleConnector(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	c, ok := connector.Default.Get(typ)
	if !ok {
		writeRPCError(w, http.StatusNotFound, fmt.Errorf("unknown connector %q", typ))
		return
	}
	d := c.Descriptor()
	writeRPCJSON(w, http.StatusOK, map[string]any{
		"name":         d.Name,
		"display_name": d.DisplayName,
		"description":  d.Description,
		"version":      d.Version,
		"categories":   d.Categories,
		"credential":   schemaToJSON(c.Credential().Schema()),
		"actions":      actionsToJSON(c.Actions()),
		"triggers":     triggersToJSON(c.Triggers()),
	})
}

func (s *rpcServer) handleInstances(w http.ResponseWriter, _ *http.Request) {
	refs, err := s.store.List()
	if err != nil {
		writeRPCError(w, http.StatusInternalServerError, err)
		return
	}
	out := []any{}
	for _, r := range refs {
		out = append(out, map[string]any{"type": r.Type, "name": r.Name})
	}
	writeRPCJSON(w, http.StatusOK, out)
}

func (s *rpcServer) handleRun(w http.ResponseWriter, r *http.Request) {
	typ := r.PathValue("type")
	name := r.PathValue("name")
	actionName := r.PathValue("action")

	params := connector.Values{}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, fmt.Errorf("read body: %w", err))
		return
	}
	if len(body) > 0 {
		var raw map[string]any
		if err := json.Unmarshal(body, &raw); err != nil {
			writeRPCError(w, http.StatusBadRequest, fmt.Errorf("body must be a JSON object: %w", err))
			return
		}
		params = connector.Values(raw)
	}

	sess, err := s.sessionFor(r.Context(), typ, name)
	if err != nil {
		if errors.Is(err, storage.ErrNotFound) {
			writeRPCError(w, http.StatusNotFound, err)
			return
		}
		// Unknown connector type — return 404 not 500.
		if isUnknownConnectorErr(err) {
			writeRPCError(w, http.StatusNotFound, err)
			return
		}
		writeRPCError(w, http.StatusInternalServerError, err)
		return
	}

	c := sess.Connector()
	var action connector.Action
	for _, a := range c.Actions() {
		if a.Name() == actionName {
			action = a
			break
		}
	}
	if action == nil {
		writeRPCError(w, http.StatusNotFound, fmt.Errorf("connector %s has no action %s", typ, actionName))
		return
	}

	// Path whitelist — same gate as the CLI's run path.
	if err := validatePaths(action.Schema(), params, fmt.Sprintf("action %s/%s.%s", typ, name, actionName)); err != nil {
		writeRPCError(w, http.StatusForbidden, err)
		return
	}

	result, err := action.Run(r.Context(), sess, params)
	if err != nil {
		writeRPCError(w, http.StatusBadRequest, err)
		return
	}
	writeRPCJSON(w, http.StatusOK, result)
}

// sessionFor returns a cached session for (type, name), opening one on
// first request. Sessions are reused across calls; the underlying
// connectors are expected to be concurrent-safe for their stateless
// actions (all current pgf connectors are — they wrap HTTP/IMAP clients
// that handle concurrency themselves).
func (s *rpcServer) sessionFor(ctx context.Context, typ, name string) (connector.Session, error) {
	key := typ + "/" + name
	s.mu.Lock()
	if sess, ok := s.sessions[key]; ok {
		s.mu.Unlock()
		return sess, nil
	}
	s.mu.Unlock()

	c, ok := connector.Default.Get(typ)
	if !ok {
		return nil, &unknownConnectorErr{typ: typ}
	}
	cred, err := s.store.Get(typ, name)
	if err != nil {
		return nil, err
	}
	opened, err := secrets.OpenValues(s.vault, c.Credential().Schema(), cred.Values)
	if err != nil {
		return nil, err
	}
	cred.Values = opened
	sess, err := c.Open(ctx, cred, connector.OpenOptions{State: s.stateMgr.For(typ, name)})
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	if existing, dup := s.sessions[key]; dup {
		s.mu.Unlock()
		_ = sess.Close()
		return existing, nil
	}
	s.sessions[key] = sess
	s.mu.Unlock()
	return sess, nil
}

// unknownConnectorErr lets handleRun map the registry miss to 404.
type unknownConnectorErr struct{ typ string }

func (e *unknownConnectorErr) Error() string { return fmt.Sprintf("unknown connector %q", e.typ) }

func isUnknownConnectorErr(err error) bool {
	var e *unknownConnectorErr
	return errors.As(err, &e)
}

// ── JSON helpers ──────────────────────────────────────────────────────────

func writeRPCJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeRPCError(w http.ResponseWriter, status int, err error) {
	writeRPCJSON(w, status, map[string]string{"error": err.Error()})
}

// schemaToJSON / actionsToJSON / triggersToJSON render the introspection
// shapes. Mirrors what `pgf actions <type>` and `pgf triggers <type>`
// print, but in machine-friendly form.
func schemaToJSON(s connector.Schema) any {
	fields := []any{}
	for _, f := range s.Fields {
		entry := map[string]any{
			"name":        f.Name,
			"label":       f.Label,
			"description": f.Description,
			"kind":        string(f.Kind),
			"required":    f.Required,
		}
		if f.Default != nil {
			entry["default"] = f.Default
		}
		if len(f.Options) > 0 {
			opts := []any{}
			for _, o := range f.Options {
				opts = append(opts, map[string]any{"value": o.Value, "label": o.Label})
			}
			entry["options"] = opts
		}
		if f.IsPath {
			entry["is_path"] = true
		}
		// ShowWhen is a predicate function; can't be serialized. The
		// field's Description is expected to mention any conditional
		// behavior (existing schemas follow this convention).
		fields = append(fields, entry)
	}
	return map[string]any{"fields": fields}
}

func actionsToJSON(actions []connector.Action) any {
	out := []any{}
	for _, a := range actions {
		out = append(out, map[string]any{
			"name":         a.Name(),
			"display_name": a.DisplayName(),
			"description":  a.Description(),
			"schema":       schemaToJSON(a.Schema()),
		})
	}
	return out
}

func triggersToJSON(triggers []connector.Trigger) any {
	out := []any{}
	for _, t := range triggers {
		out = append(out, map[string]any{
			"name":        t.Name(),
			"strategy":    string(t.Strategy()),
			"description": t.Description(),
			"schema":      schemaToJSON(t.Schema()),
		})
	}
	return out
}
