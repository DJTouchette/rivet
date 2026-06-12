package context

import (
	"errors"
	"testing"
)

func fakeRunner(stdout string) ReconRunner {
	return func(args []string) (string, string, int, error) {
		return stdout, "", 0, nil
	}
}

func TestLoadCodeDocs(t *testing.T) {
	out := `[
  {"file":"src/orders/handler.go","symbol":"ProcessPayment","line":3,"source":"comment","origin":"src/orders/handler.go","body":"Never call this inside a transaction."},
  {"file":"src/orders/handler.go","source":"sidecar","origin":"src/orders/.context/handler.md","body":"# Orders handler\n\nStatus names differ from the API."},
  {"file":"src/jobs.py","line":1,"source":"comment","origin":"src/jobs.py","body":"Cron must never run on Tuesdays."}
]`
	docs, err := LoadCodeDocs(fakeRunner(out))
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 3 {
		t.Fatalf("expected 3 docs, got %d", len(docs))
	}

	byName := map[string]*Document{}
	for _, d := range docs {
		if d.Kind != KindCode {
			t.Errorf("doc %s has kind %s, want code", d.Name, d.Kind)
		}
		byName[d.Name] = d
	}

	sym := byName["src/orders/handler.go#ProcessPayment"]
	if sym == nil {
		t.Fatalf("missing symbol doc; names: %v", keys(byName))
	}
	if sym.Title != "ProcessPayment (src/orders/handler.go)" {
		t.Errorf("symbol doc title = %q", sym.Title)
	}
	if len(sym.RelatedPaths) != 1 || sym.RelatedPaths[0] != "src/orders/handler.go" {
		t.Errorf("related paths = %v", sym.RelatedPaths)
	}

	sidecar := byName["src/orders/handler.go"]
	if sidecar == nil {
		t.Fatalf("missing sidecar doc; names: %v", keys(byName))
	}
	if sidecar.Title != "Orders handler" {
		t.Errorf("sidecar title = %q, want heading from body", sidecar.Title)
	}

	if fileLevel := byName["src/jobs.py:1"]; fileLevel == nil {
		t.Fatalf("missing line-qualified file-level doc; names: %v", keys(byName))
	}
}

func TestLoadCodeDocsEmpty(t *testing.T) {
	for _, out := range []string{"", "null\n", "[]\n"} {
		docs, err := LoadCodeDocs(fakeRunner(out))
		if err != nil {
			t.Fatalf("output %q: %v", out, err)
		}
		if len(docs) != 0 {
			t.Fatalf("output %q: expected 0 docs, got %d", out, len(docs))
		}
	}
}

func TestLoadCodeDocsErrors(t *testing.T) {
	_, err := LoadCodeDocs(func([]string) (string, string, int, error) {
		return "", "", 0, errors.New("boom")
	})
	if err == nil {
		t.Fatal("expected error from runner failure")
	}

	_, err = LoadCodeDocs(func([]string) (string, string, int, error) {
		return "", "recon exploded", 1, nil
	})
	if err == nil {
		t.Fatal("expected error from nonzero exit")
	}
}

func TestCodeDocURIAndWeight(t *testing.T) {
	d := &Document{Name: "src/a.go#F", Kind: KindCode}
	if got := d.URI(); got != "rivet://code/src/a.go#F" {
		t.Errorf("URI = %q", got)
	}
	if w := kindWeight(KindCode); w >= 1.0 || w <= kindWeight(KindWiki) {
		t.Errorf("code weight %v should sit between wiki %v and curated 1.0", w, kindWeight(KindWiki))
	}
}

func keys(m map[string]*Document) []string {
	var ks []string
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
