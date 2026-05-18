package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/sistemica/pantograf/connector"
	"golang.org/x/term"
)

// runWizard collects values for the given CredentialSpec interactively and
// returns a populated Credential. Generic — works for any connector.
//
// The single *bufio.Reader is threaded through every prompt; never wrap it
// again or buffered bytes get lost between fields.
func runWizard(ctx context.Context, typ, name string, spec connector.CredentialSpec) (connector.Credential, error) {
	in := bufio.NewReader(os.Stdin)
	values := connector.Values{}

	// 1. Preset picker (optional).
	if presets := spec.Presets(); len(presets) > 0 {
		fmt.Printf("\nAvailable presets for %s:\n", typ)
		for i, p := range presets {
			fmt.Printf("  %d. %s — %s\n", i+1, p.Name, p.Description)
		}
		choice := promptLine(in, "Pick a preset", "1")
		idx, err := strconv.Atoi(strings.TrimSpace(choice))
		if err != nil || idx < 1 || idx > len(presets) {
			return connector.Credential{}, fmt.Errorf("invalid preset choice %q", choice)
		}
		for k, v := range presets[idx-1].Values {
			values[k] = v
		}
		fmt.Printf("→ %s applied\n", presets[idx-1].Name)
	}

	// 2. Per-field prompt — values already filled by preset are shown as defaults.
	for _, f := range spec.Schema().Fields {
		// Skip fields whose ShowWhen predicate isn't satisfied by what
		// we've collected so far. Schemas list the discriminator (e.g.
		// `auth_mode`) ahead of dependent fields so the predicate fires
		// on the user's pick, not on stale defaults.
		if !f.IsActive(values) {
			continue
		}
		current, hasPreset := values[f.Name]
		def := current
		if !hasPreset {
			def = f.Default
		}
		val, err := promptField(in, f, def)
		if err != nil {
			return connector.Credential{}, err
		}
		if val != nil {
			values[f.Name] = val
		}
	}

	// 3. Connector-side derived defaults (e.g. infer smtp from imap).
	values = spec.Defaults(values)

	// 4. Validate live.
	cred := connector.Credential{Type: typ, Name: name, Values: values}
	fmt.Print("Validating... ")
	if err := spec.Validate(ctx, cred); err != nil {
		fmt.Println("✗")
		return connector.Credential{}, err
	}
	fmt.Println("✓")
	return cred, nil
}

func promptField(in *bufio.Reader, f connector.FieldSpec, def any) (any, error) {
	label := f.Label
	if label == "" {
		label = f.Name
	}

	switch f.Kind {
	case connector.FieldSecret:
		raw, err := readSecret(in, label)
		if err != nil {
			return nil, err
		}
		if raw == "" && def != nil {
			return def, nil
		}
		if raw == "" && f.Required {
			return nil, fmt.Errorf("%s is required", f.Name)
		}
		return raw, nil

	case connector.FieldEnum:
		fmt.Printf("\n%s — pick one:\n", label)
		for i, opt := range f.Options {
			marker := " "
			if def != nil && opt.Value == fmt.Sprint(def) {
				marker = "*"
			}
			fmt.Printf(" %s %d. %s (%s)\n", marker, i+1, opt.Label, opt.Value)
		}
		raw := strings.TrimSpace(promptLine(in, "Choice", defStr(def)))
		if raw == "" {
			return def, nil
		}
		if idx, err := strconv.Atoi(raw); err == nil && idx >= 1 && idx <= len(f.Options) {
			return f.Options[idx-1].Value, nil
		}
		for _, opt := range f.Options {
			if opt.Value == raw {
				return raw, nil
			}
		}
		return nil, fmt.Errorf("%s: %q is not a valid choice", f.Name, raw)

	case connector.FieldInt:
		raw := promptLine(in, label, defStr(def))
		if raw == "" {
			return def, nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", f.Name, err)
		}
		return n, nil

	case connector.FieldBool:
		raw := strings.ToLower(strings.TrimSpace(promptLine(in, label+" (y/n)", defStr(def))))
		if raw == "" {
			return def, nil
		}
		return raw == "y" || raw == "yes" || raw == "true", nil

	case connector.FieldStringList:
		raw := strings.TrimSpace(promptLine(in, label+" (comma-separated)", defStr(def)))
		if raw == "" {
			return def, nil
		}
		parts := strings.Split(raw, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out, nil

	default: // FieldString, FieldLongText
		raw := promptLine(in, label, defStr(def))
		if raw == "" {
			if def != nil {
				return def, nil
			}
			if f.Required {
				return nil, fmt.Errorf("%s is required", f.Name)
			}
			return nil, nil
		}
		return raw, nil
	}
}

// promptLine prints "Label [default]: " and reads one line from in.
func promptLine(in *bufio.Reader, label, def string) string {
	if def != "" {
		fmt.Printf("%s [%s]: ", label, def)
	} else {
		fmt.Printf("%s: ", label)
	}
	line, _ := in.ReadString('\n')
	return strings.TrimRight(line, "\r\n")
}

func defStr(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}

// readSecret reads a line without echo when stdin is a TTY; otherwise reads
// from the same buffered stdin reader the rest of the wizard uses.
func readSecret(in *bufio.Reader, label string) (string, error) {
	fmt.Printf("%s: ", label)
	if !term.IsTerminal(int(os.Stdin.Fd())) {
		line, err := in.ReadString('\n')
		return strings.TrimRight(line, "\r\n"), err
	}
	b, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}
