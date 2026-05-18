package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sistemica/pantograf/connector"
	"github.com/sistemica/pantograf/secrets"
	"github.com/sistemica/pantograf/state"
	"github.com/sistemica/pantograf/storage"
)

// cmdWebhooks hosts a multiplexed HTTP receiver for webhook triggers across
// any number of instances. Each mounted trigger gets its own URL —
// /<type>/<name>/<trigger> — and shares the listen port + the
// stdout NDJSON event stream.
//
//   pgf webhooks [flags] [<type>/<name>:<trigger> ...]
//
// Flags:
//   --addr        :8080 by default
//   --public-url  required unless --no-register
//   --no-register skip OnEnable/OnDisable; for local curl tests
//   -p key=value  applied to every trigger (each picks the keys it knows)
//
// With no positionals, every WebhookTrigger across every stored instance
// is mounted — typical "host everything" use.
//
// Distinct from `pgf serve`: this is the *inbound* side (the network's
// callers POST events to pgf). `pgf serve` is the *outbound* daemon
// (callers ask pgf to run actions for them over JSON-RPC).
func cmdWebhooks(ctx context.Context, store storage.Store, vault *secrets.Vault, stateMgr state.Manager, args []string) {
	fs := flag.NewFlagSet("webhooks", flag.ContinueOnError)
	addr := fs.String("addr", ":8080", "Listen address")
	publicURL := fs.String("public-url", "", "Public base URL the upstreams will POST to")
	noRegister := fs.Bool("no-register", false, "Skip OnEnable/OnDisable. For local dev / curl tests.")
	if err := fs.Parse(args); err != nil {
		fail(err)
	}

	// Split remaining args into refs (foo/bar:baz) and -p k=v pairs so
	// serve and watch share the same param shape.
	var refs []string
	var paramArgs []string
	rest := fs.Args()
	for i := 0; i < len(rest); i++ {
		a := rest[i]
		if a == "-p" || a == "--param" {
			if i+1 >= len(rest) {
				fail(errors.New("-p requires k=v"))
			}
			paramArgs = append(paramArgs, a, rest[i+1])
			i++
			continue
		}
		refs = append(refs, a)
	}
	params, err := parseParams(paramArgs)
	if err != nil {
		fail(err)
	}

	if *publicURL == "" && !*noRegister {
		fail(errors.New("--public-url is required (or --no-register for local dev)"))
	}

	// Resolve refs into mount specs. Empty refs ⇒ mount every webhook
	// trigger across every instance.
	mounts, err := resolveMounts(ctx, store, vault, stateMgr, refs)
	if err != nil {
		fail(err)
	}
	if len(mounts) == 0 {
		fail(errors.New("no webhook triggers to mount (no instances or no webhook triggers)"))
	}

	enc := json.NewEncoder(os.Stdout)
	emit := func(c context.Context, e connector.Event) error {
		return enc.Encode(e)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Defer all closers/disablers to run on shutdown in reverse order.
	var teardown []func()
	defer func() {
		for i := len(teardown) - 1; i >= 0; i-- {
			teardown[i]()
		}
	}()

	for _, m := range mounts {
		m := m // capture for closure
		mux.HandleFunc(m.path, func(w http.ResponseWriter, r *http.Request) {
			resp, err := m.trigger.Handle(r.Context(), m.sess, params, r, emit)
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			writeResponse(w, resp)
		})
		teardown = append(teardown, func() { _ = m.sess.Close() })
		if !*noRegister {
			full := strings.TrimRight(*publicURL, "/") + m.path
			if err := m.trigger.OnEnable(ctx, m.sess, params, full); err != nil {
				fail(fmt.Errorf("OnEnable %s: %w", m.label, err))
			}
			teardown = append(teardown, func() {
				if err := m.trigger.OnDisable(ctx, m.sess, params); err != nil {
					fmt.Fprintf(os.Stderr, "OnDisable %s: %v\n", m.label, err)
				}
			})
		}
		fmt.Fprintf(os.Stderr, "mount: %s → POST %s\n", m.label, m.path)
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		fmt.Fprintf(os.Stderr, "listening on %s, %d mounts\n", *addr, len(mounts))
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-serveCtx.Done():
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			fail(err)
		}
	}

	shutdownCtx, sc := context.WithTimeout(ctx, 5*time.Second)
	defer sc()
	_ = srv.Shutdown(shutdownCtx)
}

// writeResponse turns a *WebhookResponse into an HTTP reply. nil means
// "200 OK, empty body" — the typical case for receivers that don't need
// to return data.
func writeResponse(w http.ResponseWriter, r *connector.WebhookResponse) {
	if r == nil {
		w.WriteHeader(http.StatusOK)
		return
	}
	for k, vv := range r.Headers {
		for _, v := range vv {
			w.Header().Add(k, v)
		}
	}
	ct := r.ContentType
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	w.Header().Set("Content-Type", ct)
	status := r.Status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	if len(r.Body) > 0 {
		_, _ = w.Write(r.Body)
	}
}

// mount represents one URL → trigger binding inside cmdWebhooks.
type mount struct {
	label   string                    // "type/name::trigger" for logs
	path    string                    // /type/name/trigger
	sess    connector.Session         // opened with this instance's cred + state
	trigger connector.WebhookTrigger
}

// resolveMounts walks refs (or all instances if empty), opens each one,
// and pairs it with each WebhookTrigger the connector exposes.
func resolveMounts(ctx context.Context, store storage.Store, vault *secrets.Vault, stateMgr state.Manager, refs []string) ([]mount, error) {
	type instSel struct {
		typ, name, trigger string // trigger == "" means "all webhook triggers on this instance"
	}
	var sels []instSel
	if len(refs) == 0 {
		// Auto-discover: every instance, every webhook trigger.
		all, err := store.List()
		if err != nil {
			return nil, err
		}
		for _, r := range all {
			sels = append(sels, instSel{typ: r.Type, name: r.Name})
		}
	} else {
		for _, ref := range refs {
			tnPart, trig, _ := strings.Cut(ref, ":")
			typ, name, ok := strings.Cut(tnPart, "/")
			if !ok {
				return nil, fmt.Errorf("ref %q must be <type>/<name>[:<trigger>]", ref)
			}
			sels = append(sels, instSel{typ: typ, name: name, trigger: trig})
		}
	}

	var out []mount
	for _, sel := range sels {
		c, ok := connector.Default.Get(sel.typ)
		if !ok {
			return nil, fmt.Errorf("unknown connector: %s", sel.typ)
		}

		// Filter triggers — webhook only, optional name match.
		var picks []connector.WebhookTrigger
		for _, t := range c.Triggers() {
			wh, isWebhook := t.(connector.WebhookTrigger)
			if !isWebhook {
				continue
			}
			if sel.trigger != "" && t.Name() != sel.trigger {
				continue
			}
			picks = append(picks, wh)
		}
		if len(picks) == 0 {
			if sel.trigger != "" {
				return nil, fmt.Errorf("connector %s has no webhook trigger %q", sel.typ, sel.trigger)
			}
			continue // auto-mode: skip instances whose connector has no webhook trigger
		}

		cred, err := store.Get(sel.typ, sel.name)
		if err != nil {
			return nil, fmt.Errorf("load %s/%s: %w", sel.typ, sel.name, err)
		}
		opened, err := secrets.OpenValues(vault, c.Credential().Schema(), cred.Values)
		if err != nil {
			return nil, fmt.Errorf("open creds %s/%s: %w", sel.typ, sel.name, err)
		}
		cred.Values = opened

		sess, err := c.Open(ctx, cred, connector.OpenOptions{State: stateMgr.For(sel.typ, sel.name)})
		if err != nil {
			return nil, fmt.Errorf("open %s/%s: %w", sel.typ, sel.name, err)
		}

		for _, wh := range picks {
			out = append(out, mount{
				label:   fmt.Sprintf("%s/%s::%s", sel.typ, sel.name, wh.Name()),
				path:    fmt.Sprintf("/%s/%s/%s", sel.typ, sel.name, wh.Name()),
				sess:    sess,
				trigger: wh,
			})
		}
	}
	return out, nil
}
