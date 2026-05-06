// Command pgf is the unified CLI consumer of the pantograf connector library.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/sistemica/pantograf/connector"
	emailpkg "github.com/sistemica/pantograf/connectors/email"
	lexofficepkg "github.com/sistemica/pantograf/connectors/lexoffice"
	telegrampkg "github.com/sistemica/pantograf/connectors/telegram"
	webhookpkg "github.com/sistemica/pantograf/connectors/webhook"
	"github.com/sistemica/pantograf/secrets"
	"github.com/sistemica/pantograf/state"
	"github.com/sistemica/pantograf/state/fsstore"
	"github.com/sistemica/pantograf/storage"
	"github.com/sistemica/pantograf/storage/yamlstore"
)

func init() {
	// Connectors register themselves into connector.Default at startup.
	// Add new connectors with one line each.
	for _, reg := range []func(*connector.Registry) error{
		emailpkg.Register,
		telegrampkg.Register,
		webhookpkg.Register,
		lexofficepkg.Register,
	} {
		if err := reg(connector.Default); err != nil {
			panic(err)
		}
	}
}

const usage = `pgf — pantograf connector CLI

Usage:
  pgf connectors                         List registered connector types
  pgf actions <type>                     List actions a connector exposes
  pgf triggers <type>                    List triggers a connector exposes
  pgf connect <type> <name>              Run wizard to add a named instance
  pgf instances                          List configured instances
  pgf rm <type>/<name>                   Remove an instance
  pgf run <type>/<name> <action> [-p k=v ...]    Execute an action
  pgf watch <type>/<name> <trigger> [-p k=v ...] Subscribe to a streaming trigger; events as NDJSON
  pgf serve [flags] [<type>/<name>:<trigger> ...]
                                         Host webhook triggers (multiplexed)

Examples:
  pgf connect email personal
  pgf run email/personal read-emails -p folder=INBOX -p limit=5
  pgf watch telegram/personal messages
  pgf serve --addr :8080 --public-url https://my.host
`

func main() {
	if len(os.Args) < 2 {
		fmt.Print(usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	ctx := context.Background()
	store, err := yamlstore.New("")
	if err != nil {
		fail(err)
	}
	vault, err := secrets.Load()
	if err != nil {
		fail(err)
	}
	stateMgr, err := fsstore.New("")
	if err != nil {
		fail(err)
	}

	switch cmd {
	case "connectors":
		cmdConnectors()
	case "actions":
		cmdActions(args)
	case "triggers":
		cmdTriggers(args)
	case "connect":
		cmdConnect(ctx, store, vault, args)
	case "instances":
		cmdInstances(store)
	case "rm":
		cmdRemove(store, args)
	case "run":
		cmdRun(ctx, store, vault, stateMgr, args)
	case "watch":
		cmdWatch(ctx, store, vault, stateMgr, args)
	case "serve":
		cmdServe(ctx, store, vault, stateMgr, args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Printf("unknown command: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
}

func cmdConnectors() {
	for _, d := range connector.Default.List() {
		fmt.Printf("%-12s %s\n             %s\n", d.Name, d.DisplayName, d.Description)
	}
}

func cmdTriggers(args []string) {
	if len(args) != 1 {
		fail(errors.New("usage: pgf triggers <type>"))
	}
	c, ok := connector.Default.Get(args[0])
	if !ok {
		fail(fmt.Errorf("unknown connector: %s", args[0]))
	}
	triggers := c.Triggers()
	if len(triggers) == 0 {
		fmt.Printf("(connector %s has no triggers)\n", args[0])
		return
	}
	for _, t := range triggers {
		fmt.Printf("%-16s [%s] %s\n", t.Name(), t.Strategy(), t.Description())
		for _, f := range t.Schema().Fields {
			req := ""
			if f.Required {
				req = " (required)"
			}
			def := ""
			if f.Default != nil {
				def = fmt.Sprintf(" [default: %v]", f.Default)
			}
			fmt.Printf("    %-14s %s%s%s\n", f.Name, f.Kind, def, req)
		}
	}
}

func cmdActions(args []string) {
	if len(args) != 1 {
		fail(errors.New("usage: pgf actions <type>"))
	}
	c, ok := connector.Default.Get(args[0])
	if !ok {
		fail(fmt.Errorf("unknown connector: %s", args[0]))
	}
	for _, a := range c.Actions() {
		fmt.Printf("%-16s %s\n", a.Name(), a.Description())
		for _, f := range a.Schema().Fields {
			req := ""
			if f.Required {
				req = " (required)"
			}
			def := ""
			if f.Default != nil {
				def = fmt.Sprintf(" [default: %v]", f.Default)
			}
			fmt.Printf("    %-14s %s%s%s\n", f.Name, f.Kind, def, req)
		}
	}
}

func cmdConnect(ctx context.Context, store storage.Store, vault *secrets.Vault, args []string) {
	// Flags must come before <type> <name>.
	var inputArg string
	var noValidate bool
	pos := []string{}
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--input" || a == "-i":
			if i+1 >= len(args) {
				fail(errors.New("--input requires a JSON value (or @file)"))
			}
			i++
			inputArg = args[i]
		case a == "--no-validate":
			noValidate = true
		default:
			pos = append(pos, a)
		}
	}
	if len(pos) != 2 {
		fail(errors.New("usage: pgf connect [--input '{...}' [--no-validate]] <type> <name>"))
	}
	typ, name := pos[0], pos[1]

	c, ok := connector.Default.Get(typ)
	if !ok {
		fail(fmt.Errorf("unknown connector: %s", typ))
	}

	var cred connector.Credential
	if inputArg != "" {
		// Non-interactive: provisioning by a script or human, no agent
		// involvement here (agents shouldn't write credentials).
		raw, err := readJSONInput(inputArg)
		if err != nil {
			fail(err)
		}
		spec := c.Credential()
		values := connector.Values(raw)
		values = spec.Defaults(values)
		cred = connector.Credential{Type: typ, Name: name, Values: values}
		if !noValidate {
			fmt.Print("Validating... ")
			if err := spec.Validate(ctx, cred); err != nil {
				fmt.Println("✗")
				fail(err)
			}
			fmt.Println("✓")
		}
	} else {
		var err error
		cred, err = runWizard(ctx, typ, name, c.Credential())
		if err != nil {
			fail(err)
		}
	}

	sealed, err := secrets.SealValues(vault, c.Credential().Schema(), cred.Values)
	if err != nil {
		fail(err)
	}
	cred.Values = sealed
	if err := store.Put(cred); err != nil {
		fail(err)
	}
	fmt.Printf("Saved %s/%s\n", typ, name)
}

func cmdInstances(store storage.Store) {
	refs, err := store.List()
	if err != nil {
		fail(err)
	}
	if len(refs) == 0 {
		fmt.Println("No instances. Use `pgf connect <type> <name>` to add one.")
		return
	}
	for _, r := range refs {
		fmt.Printf("%s/%s\n", r.Type, r.Name)
	}
}

func cmdRemove(store storage.Store, args []string) {
	if len(args) != 1 {
		fail(errors.New("usage: pgf rm <type>/<name>"))
	}
	typ, name, ok := strings.Cut(args[0], "/")
	if !ok {
		fail(errors.New("argument must be <type>/<name>"))
	}
	if err := store.Delete(typ, name); err != nil {
		fail(err)
	}
	fmt.Printf("Removed %s/%s\n", typ, name)
}

func cmdRun(ctx context.Context, store storage.Store, vault *secrets.Vault, stateMgr state.Manager, args []string) {
	if len(args) < 2 {
		fail(errors.New("usage: pgf run <type>/<name> <action> [-p k=v ...]"))
	}
	typ, name, ok := strings.Cut(args[0], "/")
	if !ok {
		fail(errors.New("first argument must be <type>/<name>"))
	}
	actionName := args[1]
	params, err := parseParams(args[2:])
	if err != nil {
		fail(err)
	}

	c, ok := connector.Default.Get(typ)
	if !ok {
		fail(fmt.Errorf("unknown connector: %s", typ))
	}
	var action connector.Action
	for _, a := range c.Actions() {
		if a.Name() == actionName {
			action = a
			break
		}
	}
	if action == nil {
		fail(fmt.Errorf("connector %s has no action %s", typ, actionName))
	}

	cred, err := store.Get(typ, name)
	if err != nil {
		fail(err)
	}
	// Decrypt FieldSecret values before opening the session.
	opened, err := secrets.OpenValues(vault, c.Credential().Schema(), cred.Values)
	if err != nil {
		fail(err)
	}
	cred.Values = opened

	sess, err := c.Open(ctx, cred, connector.OpenOptions{State: stateMgr.For(typ, name)})
	if err != nil {
		fail(err)
	}
	defer sess.Close()

	result, err := action.Run(ctx, sess, params)
	if err != nil {
		fail(err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(result)
}

// parseParams turns ["-p", "k=v", "-p", "k2=v2"] into Values. A repeated
// key promotes the value to a []string — so list-typed fields can be passed
// either as comma-separated ("-p to=a,b") or by repeating ("-p to=a -p to=b").
//
// Also accepts --input '{"k": "v"}' (or --input @path/to/file.json) as an
// alternative for nested/complex params. -p and --input can be mixed; -p
// takes precedence on key conflicts.
func parseParams(args []string) (connector.Values, error) {
	// Two passes: --input fills the base, then -p overrides. Order in the
	// command line doesn't matter — -p always wins on key conflicts.
	base := connector.Values{}
	overrides := connector.Values{}
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case a == "--input" || a == "-i":
			if i+1 >= len(args) {
				return nil, errors.New("--input requires a JSON value (or @file)")
			}
			i++
			parsed, err := readJSONInput(args[i])
			if err != nil {
				return nil, err
			}
			for k, v := range parsed {
				base[k] = v
			}
		case a == "-p" || a == "--param":
			if i+1 >= len(args) {
				return nil, errors.New("-p requires k=v")
			}
			i++
			k, v, ok := strings.Cut(args[i], "=")
			if !ok {
				return nil, fmt.Errorf("expected k=v, got %q", args[i])
			}
			if existing, present := overrides[k]; present {
				switch e := existing.(type) {
				case string:
					overrides[k] = []string{e, v}
				case []string:
					overrides[k] = append(e, v)
				default:
					overrides[k] = v
				}
			} else {
				overrides[k] = v
			}
		default:
			return nil, fmt.Errorf("unexpected arg %q", a)
		}
		i++
	}
	for k, v := range overrides {
		base[k] = v
	}
	return base, nil
}

// readJSONInput parses a JSON object from a literal string or @file path.
// Returns map[string]any so callers can iterate keys.
func readJSONInput(arg string) (map[string]any, error) {
	raw := arg
	if strings.HasPrefix(raw, "@") {
		path := strings.TrimPrefix(raw, "@")
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read --input file %q: %w", path, err)
		}
		raw = string(data)
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("parse --input as JSON object: %w", err)
	}
	return out, nil
}

// cmdWatch subscribes to a trigger and writes NDJSON events to stdout
// until SIGINT/SIGTERM. Each line is a single Event.
func cmdWatch(ctx context.Context, store storage.Store, vault *secrets.Vault, stateMgr state.Manager, args []string) {
	if len(args) < 2 {
		fail(errors.New("usage: pgf watch <type>/<name> <trigger> [-p k=v ...]"))
	}
	typ, name, ok := strings.Cut(args[0], "/")
	if !ok {
		fail(errors.New("first argument must be <type>/<name>"))
	}
	trigName := args[1]
	params, err := parseParams(args[2:])
	if err != nil {
		fail(err)
	}

	c, ok := connector.Default.Get(typ)
	if !ok {
		fail(fmt.Errorf("unknown connector: %s", typ))
	}
	var trig connector.Trigger
	for _, t := range c.Triggers() {
		if t.Name() == trigName {
			trig = t
			break
		}
	}
	if trig == nil {
		fail(fmt.Errorf("connector %s has no trigger %s", typ, trigName))
	}

	cred, err := store.Get(typ, name)
	if err != nil {
		fail(err)
	}
	opened, err := secrets.OpenValues(vault, c.Credential().Schema(), cred.Values)
	if err != nil {
		fail(err)
	}
	cred.Values = opened

	sess, err := c.Open(ctx, cred, connector.OpenOptions{State: stateMgr.For(typ, name)})
	if err != nil {
		fail(err)
	}
	defer sess.Close()

	watchCtx, cancel := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer cancel()

	enc := json.NewEncoder(os.Stdout)
	emit := func(c context.Context, e connector.Event) error {
		return enc.Encode(e)
	}

	streaming, ok := trig.(connector.StreamingTrigger)
	if !ok {
		fail(fmt.Errorf("trigger %s has strategy %q; use 'pgf serve' for webhook triggers", trigName, trig.Strategy()))
	}
	if err := streaming.Subscribe(watchCtx, sess, params, emit); err != nil && !errors.Is(err, context.Canceled) {
		fail(err)
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
