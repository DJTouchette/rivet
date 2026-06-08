package semantic

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
	"testing"
)

// fakeEmbedder is a deterministic bag-of-words embedder for tests. Each word
// bumps a few dimensions, so texts that share vocabulary have higher cosine
// similarity — enough to exercise ranking without a real model.
type fakeEmbedder struct {
	id  string
	dim int
}

func newFake() *fakeEmbedder { return &fakeEmbedder{id: "fake:v1", dim: 64} }

func (f *fakeEmbedder) ID() string { return f.id }
func (f *fakeEmbedder) Dim() int   { return f.dim }

func (f *fakeEmbedder) Embed(_ context.Context, texts []string) ([]Vector, error) {
	out := make([]Vector, len(texts))
	for i, t := range texts {
		v := make(Vector, f.dim)
		for _, w := range strings.Fields(strings.ToLower(t)) {
			h := fnv.New32a()
			h.Write([]byte(w))
			sum := h.Sum32()
			v[sum%uint32(f.dim)] += 1
			v[(sum/7)%uint32(f.dim)] += 0.5
		}
		out[i] = v
	}
	return out, nil
}

func TestCosine(t *testing.T) {
	a := Vector{1, 0, 0}
	if got := cosine(a, a); math.Abs(got-1) > 1e-9 {
		t.Errorf("cosine(a,a) = %v, want 1", got)
	}
	if got := cosine(Vector{1, 0}, Vector{0, 1}); math.Abs(got) > 1e-9 {
		t.Errorf("orthogonal cosine = %v, want 0", got)
	}
	if got := cosine(Vector{1, 1}, Vector{-1, -1}); math.Abs(got+1) > 1e-9 {
		t.Errorf("opposite cosine = %v, want -1", got)
	}
	// Mismatched / degenerate inputs are safe.
	if cosine(Vector{1, 2, 3}, Vector{1, 2}) != 0 {
		t.Error("mismatched dims should be 0")
	}
	if cosine(Vector{0, 0}, Vector{0, 0}) != 0 {
		t.Error("zero norm should be 0")
	}
}

func TestNewFactory(t *testing.T) {
	// Disabled backend → nil embedder, no error.
	emb, err := New(Config{Backend: BackendNone})
	if err != nil || emb != nil {
		t.Errorf("BackendNone = (%v, %v), want (nil, nil)", emb, err)
	}
	// Unknown backend → error.
	if _, err := New(Config{Backend: "voyageXYZ"}); err == nil {
		t.Error("unknown backend should error")
	}
	// HTTP backends construct without contacting anything.
	if e, err := New(Config{Backend: BackendOpenAI}); err != nil || e == nil {
		t.Errorf("openai construct = (%v, %v)", e, err)
	}
	if e, err := New(Config{Backend: BackendOllama}); err != nil || e == nil {
		t.Errorf("ollama construct = (%v, %v)", e, err)
	}
	// ONNX without the build tag is unavailable (stub).
	if _, err := New(Config{Backend: BackendONNX, Model: "/tmp/x"}); err == nil {
		t.Error("onnx stub should report unavailable without -tags onnx")
	}
}

func TestHTTPEmbedderID(t *testing.T) {
	// Same model on different hosts must have different identities so their
	// vectors never get compared.
	a, _ := newHTTPEmbedder(httpOpenAI, Config{Model: "m", BaseURL: "https://a.example/v1"})
	b, _ := newHTTPEmbedder(httpOpenAI, Config{Model: "m", BaseURL: "https://b.example/v1"})
	if a.ID() == b.ID() {
		t.Errorf("different hosts shared ID %q", a.ID())
	}
}
