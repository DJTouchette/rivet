package semantic

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := OpenStore(dir, "fake:v1", 0)
	if err != nil {
		t.Fatal(err)
	}

	h1 := HashText("fake:v1", "alpha")
	h2 := HashText("fake:v1", "beta")
	if err := st.Put(h1, Vector{1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	if err := st.Put(h2, Vector{5, 6, 7, 8}); err != nil {
		t.Fatal(err)
	}
	if !st.Dirty() {
		t.Error("store should be dirty after Put")
	}
	if err := st.Save(); err != nil {
		t.Fatal(err)
	}
	if st.Dirty() {
		t.Error("store should be clean after Save")
	}

	// Manifest and packed blob exist (both committable to git).
	if _, err := os.Stat(filepath.Join(dir, manifestName)); err != nil {
		t.Errorf("manifest missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, vectorsName)); err != nil {
		t.Errorf("vectors.bin missing: %v", err)
	}

	// Reload and verify vectors survive byte-exact.
	re, err := OpenStore(dir, "fake:v1", 4)
	if err != nil {
		t.Fatal(err)
	}
	if re.Len() != 2 {
		t.Fatalf("reloaded %d vectors, want 2", re.Len())
	}
	v, ok := re.Get(h1)
	if !ok || len(v) != 4 || v[0] != 1 || v[3] != 4 {
		t.Errorf("reloaded h1 = %v, ok=%v", v, ok)
	}
}

func TestStoreDeterministicSave(t *testing.T) {
	// Two stores with the same content, inserted in different order, must
	// produce byte-identical files — the property that makes them safe to
	// commit and diff.
	build := func(order []int) (string, string) {
		dir := t.TempDir()
		st, _ := OpenStore(dir, "fake:v1", 0)
		texts := []string{"one", "two", "three"}
		vecs := []Vector{{1, 1}, {2, 2}, {3, 3}}
		for _, i := range order {
			st.Put(HashText("fake:v1", texts[i]), vecs[i])
		}
		if err := st.Save(); err != nil {
			t.Fatal(err)
		}
		mf, _ := os.ReadFile(filepath.Join(dir, manifestName))
		bin, _ := os.ReadFile(filepath.Join(dir, vectorsName))
		return string(mf), string(bin)
	}

	mfA, binA := build([]int{0, 1, 2})
	mfB, binB := build([]int{2, 0, 1})
	if mfA != mfB {
		t.Error("manifest differs by insertion order; not deterministic")
	}
	if binA != binB {
		t.Error("packed vectors differ by insertion order; not deterministic")
	}
}

func TestStoreModelSwitchInvalidates(t *testing.T) {
	dir := t.TempDir()
	st, _ := OpenStore(dir, "modelA", 0)
	st.Put(HashText("modelA", "x"), Vector{1, 2})
	st.Save()

	// Opening with a different model id discards the incompatible cache.
	switched, err := OpenStore(dir, "modelB", 0)
	if err != nil {
		t.Fatal(err)
	}
	if switched.Len() != 0 {
		t.Errorf("model switch should start empty, got %d entries", switched.Len())
	}
}

func TestStoreDimMismatchRejected(t *testing.T) {
	st, _ := OpenStore(t.TempDir(), "m", 0)
	if err := st.Put("h1", Vector{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	if err := st.Put("h2", Vector{1, 2}); err == nil {
		t.Error("expected dim-mismatch error on second Put")
	}
}
