// Package pins defines the protocol by which sibling tools (rally, witness,
// recon, ...) contribute "pinned to chat" items that rivet exposes as MCP
// resources. Pinning means an item is part of the user's active working
// context and should be auto-injected into agent conversations until unpinned.
package pins

import (
	"fmt"
	"strings"
)

// Item is a single pinned thing — a ticket, a runbook, a search result, etc.
// The URI uniquely identifies the item across all providers and is what the
// MCP client uses to read its body.
type Item struct {
	URI         string
	Name        string
	Description string
	MimeType    string
	Body        string // resolved when fetched via Read
}

// Provider is implemented by any sibling tool that wants to contribute pinned
// items. Source returns a short identifier ("rally", "witness") used for
// URI routing. Implementations must produce URIs that begin with their source
// (e.g. "rally://pinned/RAL-123") so the registry can dispatch reads.
type Provider interface {
	Source() string
	List() ([]Item, error)
	Read(uri string) (Item, error)
}

// Writer is implemented by providers that allow agent-driven pin/unpin from
// inside an MCP session. Providers may implement Provider only (read-only) or
// both Provider and Writer.
type Writer interface {
	Pin(id, note string) error
	Unpin(id string) error
}

// Registry aggregates pinned items from multiple providers.
type Registry struct {
	providers []Provider
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{}
}

// Add registers a provider. Duplicate sources are not deduplicated — the
// caller is responsible for not registering the same provider twice.
func (r *Registry) Add(p Provider) {
	r.providers = append(r.providers, p)
}

// List returns every pinned item across all providers. Provider errors are
// returned individually rather than aggregated, so a failing rally adapter
// doesn't suppress witness pins. The first error encountered is returned but
// successful results are always included up to that point.
func (r *Registry) List() ([]Item, error) {
	var out []Item
	for _, p := range r.providers {
		items, err := p.List()
		if err != nil {
			return out, fmt.Errorf("pins: provider %q: %w", p.Source(), err)
		}
		out = append(out, items...)
	}
	return out, nil
}

// Read returns the body of a single pinned item, routing by URI scheme to
// the matching provider.
func (r *Registry) Read(uri string) (Item, error) {
	src, ok := sourceFromURI(uri)
	if !ok {
		return Item{}, fmt.Errorf("pins: malformed URI %q (expected source://...)", uri)
	}
	for _, p := range r.providers {
		if p.Source() == src {
			return p.Read(uri)
		}
	}
	return Item{}, fmt.Errorf("pins: no provider registered for source %q", src)
}

// WriterFor returns the Writer for a source if the registered provider
// supports writes.
func (r *Registry) WriterFor(source string) (Writer, bool) {
	for _, p := range r.providers {
		if p.Source() != source {
			continue
		}
		if w, ok := p.(Writer); ok {
			return w, true
		}
	}
	return nil, false
}

// Has reports whether a URI belongs to any registered provider's pins.
func (r *Registry) Has(uri string) bool {
	src, ok := sourceFromURI(uri)
	if !ok {
		return false
	}
	for _, p := range r.providers {
		if p.Source() == src {
			items, err := p.List()
			if err != nil {
				return false
			}
			for _, it := range items {
				if it.URI == uri {
					return true
				}
			}
		}
	}
	return false
}

func sourceFromURI(uri string) (string, bool) {
	idx := strings.Index(uri, "://")
	if idx <= 0 {
		return "", false
	}
	return uri[:idx], true
}
