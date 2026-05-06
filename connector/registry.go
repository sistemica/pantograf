package connector

import (
	"fmt"
	"sort"
	"sync"
)

// Registry holds the connector types compiled into a binary. In-process,
// global lookup by name. Thread-safe so an HTTP server can read while a
// startup goroutine is still registering.
type Registry struct {
	mu  sync.RWMutex
	all map[string]Connector
}

func NewRegistry() *Registry {
	return &Registry{all: map[string]Connector{}}
}

// Register adds a connector. Duplicate names are an error — the caller is
// expected to surface this at startup, not silently overwrite.
func (r *Registry) Register(c Connector) error {
	d := c.Descriptor()
	if d.Name == "" {
		return fmt.Errorf("connector has empty Descriptor.Name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, dup := r.all[d.Name]; dup {
		return fmt.Errorf("connector %q already registered", d.Name)
	}
	r.all[d.Name] = c
	return nil
}

func (r *Registry) Get(name string) (Connector, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.all[name]
	return c, ok
}

// List returns descriptors sorted by Name for stable CLI/UI output.
func (r *Registry) List() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]Descriptor, 0, len(r.all))
	for _, c := range r.all {
		out = append(out, c.Descriptor())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// MustRegister is the package-init convenience. Panics on duplicate, which
// is the right behaviour during process startup.
func (r *Registry) MustRegister(c Connector) {
	if err := r.Register(c); err != nil {
		panic(err)
	}
}

// Default is the process-global registry. Connectors register into it via
// blank import + init(). The CLI binary controls which connectors compile
// in by which packages it imports.
var Default = NewRegistry()
