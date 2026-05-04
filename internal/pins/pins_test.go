package pins

import (
	"errors"
	"testing"
)

type fakeProvider struct {
	src     string
	items   []Item
	listErr error
	readErr error
}

func (f *fakeProvider) Source() string { return f.src }
func (f *fakeProvider) List() ([]Item, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.items, nil
}
func (f *fakeProvider) Read(uri string) (Item, error) {
	if f.readErr != nil {
		return Item{}, f.readErr
	}
	for _, it := range f.items {
		if it.URI == uri {
			return it, nil
		}
	}
	return Item{}, errors.New("not found")
}

func TestRegistry_AggregatesAcrossProviders(t *testing.T) {
	r := NewRegistry()
	r.Add(&fakeProvider{src: "rally", items: []Item{{URI: "rally://pinned/RAL-1", Name: "ticket"}}})
	r.Add(&fakeProvider{src: "witness", items: []Item{{URI: "witness://pinned/run-42", Name: "runbook"}}})

	items, err := r.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d", len(items))
	}
}

func TestRegistry_ReadRoutesByURIScheme(t *testing.T) {
	rally := &fakeProvider{src: "rally", items: []Item{{URI: "rally://pinned/RAL-1", Body: "rally body"}}}
	witness := &fakeProvider{src: "witness", items: []Item{{URI: "witness://pinned/run-42", Body: "witness body"}}}

	r := NewRegistry()
	r.Add(rally)
	r.Add(witness)

	got, err := r.Read("rally://pinned/RAL-1")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Body != "rally body" {
		t.Fatalf("expected rally body, got %q", got.Body)
	}

	got, err = r.Read("witness://pinned/run-42")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if got.Body != "witness body" {
		t.Fatalf("expected witness body, got %q", got.Body)
	}
}

func TestRegistry_ReadUnknownSource(t *testing.T) {
	r := NewRegistry()
	r.Add(&fakeProvider{src: "rally"})

	_, err := r.Read("witness://pinned/x")
	if err == nil {
		t.Fatal("expected error for unknown source")
	}
}

func TestRegistry_ReadMalformedURI(t *testing.T) {
	r := NewRegistry()
	_, err := r.Read("not a uri")
	if err == nil {
		t.Fatal("expected error for malformed URI")
	}
}

func TestRegistry_HasURI(t *testing.T) {
	r := NewRegistry()
	r.Add(&fakeProvider{src: "rally", items: []Item{{URI: "rally://pinned/RAL-1"}}})

	if !r.Has("rally://pinned/RAL-1") {
		t.Fatal("Has should return true for known URI")
	}
	if r.Has("rally://pinned/RAL-99") {
		t.Fatal("Has should return false for unknown URI under known source")
	}
	if r.Has("witness://pinned/x") {
		t.Fatal("Has should return false for unknown source")
	}
}

func TestRegistry_ListPropagatesProviderError(t *testing.T) {
	r := NewRegistry()
	r.Add(&fakeProvider{src: "rally", items: []Item{{URI: "rally://pinned/RAL-1"}}})
	r.Add(&fakeProvider{src: "broken", listErr: errors.New("boom")})

	items, err := r.List()
	if err == nil {
		t.Fatal("expected error from broken provider")
	}
	// Items from the healthy provider before the failing one should still be returned.
	if len(items) != 1 {
		t.Fatalf("expected 1 item before failing provider, got %d", len(items))
	}
}
