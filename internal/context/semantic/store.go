package semantic

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// Store is a content-hash-keyed cache of vectors that round-trips to disk in a
// form designed to live in git:
//
//   - manifest.json — a sorted, human-diffable map of contentHash -> slot index,
//     plus the model id and dimension. Sorted keys mean a re-index produces a
//     minimal diff.
//   - vectors.bin — slots of `dim` little-endian float32 values, packed back to
//     back. Binary, but deterministic for a given (model, text): the same input
//     always hashes to the same slot, so unchanged content never moves.
//
// Because the key includes the embedder ID (see HashText), vectors produced by
// a different model can never collide with — or be compared against — these.
type Store struct {
	model string
	dim   int
	index map[string]int // contentHash -> slot
	vecs  []Vector       // slot -> vector
	dir   string
	dirty bool
}

type manifest struct {
	Model   string         `json:"model"`
	Dim     int            `json:"dim"`
	Vectors map[string]int `json:"vectors"` // contentHash -> slot
}

const (
	manifestName = "manifest.json"
	vectorsName  = "vectors.bin"
)

// HashText is the cache key for a piece of text under a given embedder. The
// model ID is mixed in so switching models invalidates every entry rather than
// silently mixing incompatible vector spaces.
func HashText(modelID, text string) string {
	h := sha256.New()
	h.Write([]byte(modelID))
	h.Write([]byte{0})
	h.Write([]byte(text))
	return hex.EncodeToString(h.Sum(nil))
}

// OpenStore loads a store from dir, or returns an empty one if dir has no
// manifest yet. A manifest whose model differs from modelID, or whose dimension
// differs from dim (when dim > 0), is discarded and rebuilt — that is the
// model-switch path. dim may be 0 if the caller does not yet know it.
func OpenStore(dir, modelID string, dim int) (*Store, error) {
	st := &Store{
		model: modelID,
		dim:   dim,
		index: map[string]int{},
		dir:   dir,
	}

	mfPath := filepath.Join(dir, manifestName)
	raw, err := os.ReadFile(mfPath)
	if os.IsNotExist(err) {
		return st, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}

	var mf manifest
	if err := json.Unmarshal(raw, &mf); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	// A different model or dimension means the cached space is incompatible:
	// drop it and start fresh rather than serve mismatched vectors.
	if mf.Model != modelID || (dim > 0 && mf.Dim != dim) {
		return st, nil
	}

	vecs, err := readPacked(filepath.Join(dir, vectorsName), mf.Dim, len(mf.Vectors))
	if err != nil {
		return nil, err
	}
	st.dim = mf.Dim
	st.index = mf.Vectors
	st.vecs = vecs
	return st, nil
}

// Get returns the cached vector for a content hash.
func (st *Store) Get(hash string) (Vector, bool) {
	slot, ok := st.index[hash]
	if !ok || slot < 0 || slot >= len(st.vecs) {
		return nil, false
	}
	return st.vecs[slot], true
}

// Put inserts or replaces the vector for a content hash. The first Put fixes
// the store's dimension; later Puts of a different length are rejected.
func (st *Store) Put(hash string, v Vector) error {
	if st.dim == 0 {
		st.dim = len(v)
	}
	if len(v) != st.dim {
		return fmt.Errorf("semantic: vector dim %d != store dim %d", len(v), st.dim)
	}
	if slot, ok := st.index[hash]; ok {
		st.vecs[slot] = v
		st.dirty = true
		return nil
	}
	st.index[hash] = len(st.vecs)
	st.vecs = append(st.vecs, v)
	st.dirty = true
	return nil
}

// Len reports how many vectors are cached.
func (st *Store) Len() int { return len(st.vecs) }

// Dim reports the store's vector dimension (0 until the first vector lands).
func (st *Store) Dim() int { return st.dim }

// Dirty reports whether there are unsaved changes.
func (st *Store) Dirty() bool { return st.dirty }

// Save writes the manifest and packed vectors to the store directory. Slots are
// renumbered into manifest-key order so the on-disk layout is a deterministic
// function of content — two machines that index the same corpus produce
// byte-identical files, which is what makes committing them sane.
func (st *Store) Save() error {
	if err := os.MkdirAll(st.dir, 0o755); err != nil {
		return err
	}

	hashes := make([]string, 0, len(st.index))
	for h := range st.index {
		hashes = append(hashes, h)
	}
	sort.Strings(hashes)

	packed := make([]Vector, len(hashes))
	remap := make(map[string]int, len(hashes))
	for newSlot, h := range hashes {
		packed[newSlot] = st.vecs[st.index[h]]
		remap[h] = newSlot
	}

	if err := writePacked(filepath.Join(st.dir, vectorsName), packed, st.dim); err != nil {
		return err
	}

	mf := manifest{Model: st.model, Dim: st.dim, Vectors: remap}
	raw, err := json.MarshalIndent(mf, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(st.dir, manifestName), append(raw, '\n'), 0o644); err != nil {
		return err
	}

	// Adopt the renumbered layout so an in-memory store stays consistent with
	// what we just wrote.
	st.index = remap
	st.vecs = packed
	st.dirty = false
	return nil
}

// writePacked writes vectors as little-endian float32, dim values per vector.
func writePacked(path string, vecs []Vector, dim int) error {
	buf := make([]byte, 0, len(vecs)*dim*4)
	tmp := make([]byte, 4)
	for _, v := range vecs {
		for i := 0; i < dim; i++ {
			var f float32
			if i < len(v) {
				f = v[i]
			}
			binary.LittleEndian.PutUint32(tmp, math.Float32bits(f))
			buf = append(buf, tmp...)
		}
	}
	return os.WriteFile(path, buf, 0o644)
}

// readPacked reads count vectors of dim little-endian float32 each.
func readPacked(path string, dim, count int) ([]Vector, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read vectors: %w", err)
	}
	need := count * dim * 4
	if len(raw) < need {
		return nil, fmt.Errorf("vectors.bin too short: have %d bytes, need %d", len(raw), need)
	}
	vecs := make([]Vector, count)
	off := 0
	for s := 0; s < count; s++ {
		v := make(Vector, dim)
		for i := 0; i < dim; i++ {
			v[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[off : off+4]))
			off += 4
		}
		vecs[s] = v
	}
	return vecs, nil
}
