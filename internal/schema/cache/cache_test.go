package cache

import (
	"testing"
	"time"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func TestSafeFileName(t *testing.T) {
	cases := map[string]string{
		"prod":      "prod",
		"prod/db":   "prod_db",
		"a b.c":     "a_b_c",
		"my-db_1":   "my-db_1",
		"":          "default",
		"../escape": "___escape",
	}
	for in, want := range cases {
		if got := safeFileName(in); got != want {
			t.Errorf("safeFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}

	// Loading something absent returns (nil, nil), not an error.
	if e, err := st.Load("missing"); err != nil || e != nil {
		t.Fatalf("Load(missing) = %v, %v; want nil, nil", e, err)
	}

	in := &Entry{Name: "prod", Engine: types.EnginePostgres, Host: "db.local", FetchedAt: time.Now().UTC().Truncate(time.Second)}
	if err := st.Save(in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	out, err := st.Load("prod")
	if err != nil || out == nil {
		t.Fatalf("Load: %v / %v", out, err)
	}
	if out.Name != "prod" || out.Engine != types.EnginePostgres || out.Host != "db.local" {
		t.Errorf("round-trip mismatch: %+v", out)
	}

	names, err := st.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	found := false
	for _, n := range names {
		if n == "prod" {
			found = true
		}
	}
	if !found {
		t.Errorf("List() = %v, want it to include prod", names)
	}
}
