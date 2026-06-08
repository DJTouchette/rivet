//go:build onnx

// This file is the real bundled-model embedder, compiled only with `-tags onnx`.
// It depends on:
//
//   - github.com/yalue/onnxruntime_go  (go get it)
//   - the ONNX Runtime shared library, located via the RIVET_EMBED_ORT_LIB env
//     var (path to libonnxruntime.so / .dylib / .dll)
//   - a model directory (Config.Model) containing:
//     model.onnx  — a sentence-embedding model (e.g. bge-small-en-v1.5)
//     vocab.txt   — the WordPiece vocabulary, one token per line
//
// It auto-discovers the model's input/output names, so exports that omit
// token_type_ids or expose a pre-pooled sentence embedding both work. Output is
// mean-pooled (for token-level last_hidden_state) and L2-normalised — the
// standard recipe for BERT-family encoders.
package semantic

import (
	"bufio"
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

// EnvORTLib points at the ONNX Runtime shared library.
const EnvORTLib = "RIVET_EMBED_ORT_LIB"

type onnxEmbedder struct {
	modelDir string
	tok      *wordPiece
	maxLen   int

	inputNames []string // subset of input_ids/attention_mask/token_type_ids present
	hasMask    bool
	hasTypes   bool
	outputName string

	mu      sync.Mutex
	session *ort.DynamicAdvancedSession
	dim     int
}

func newONNXEmbedder(cfg Config) (Embedder, error) {
	if cfg.Model == "" {
		return nil, fmt.Errorf("%w: onnx backend needs Model set to a model directory", ErrUnavailable)
	}
	tok, err := loadWordPiece(filepath.Join(cfg.Model, "vocab.txt"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}

	if !ort.IsInitialized() {
		if lib := os.Getenv(EnvORTLib); lib != "" {
			ort.SetSharedLibraryPath(lib)
		}
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("%w: init onnxruntime (set %s to libonnxruntime.so): %v", ErrUnavailable, EnvORTLib, err)
		}
	}

	modelPath := filepath.Join(cfg.Model, "model.onnx")
	inInfo, outInfo, err := ort.GetInputOutputInfo(modelPath)
	if err != nil {
		return nil, fmt.Errorf("%w: inspect model: %v", ErrUnavailable, err)
	}

	e := &onnxEmbedder{modelDir: cfg.Model, tok: tok, maxLen: 256}
	have := map[string]bool{}
	for _, in := range inInfo {
		have[in.Name] = true
	}
	// input_ids is mandatory; mask and token_type_ids are included only if the
	// model declares them.
	for _, name := range []string{"input_ids", "attention_mask", "token_type_ids"} {
		if have[name] {
			e.inputNames = append(e.inputNames, name)
		}
	}
	if !have["input_ids"] {
		return nil, fmt.Errorf("%w: model has no input_ids input (inputs: %v)", ErrUnavailable, names(inInfo))
	}
	e.hasMask = have["attention_mask"]
	e.hasTypes = have["token_type_ids"]
	if len(outInfo) == 0 {
		return nil, fmt.Errorf("%w: model declares no outputs", ErrUnavailable)
	}
	e.outputName = outInfo[0].Name

	sess, err := ort.NewDynamicAdvancedSession(modelPath, e.inputNames, []string{e.outputName}, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: load model: %v", ErrUnavailable, err)
	}
	e.session = sess
	return e, nil
}

func names(infos []ort.InputOutputInfo) []string {
	out := make([]string, len(infos))
	for i, in := range infos {
		out[i] = in.Name
	}
	return out
}

// ID identifies the model by name (the model directory's base name), not its
// absolute path, so a committed .rivet/embeddings/ cache stays valid across
// machines that keep the model in different locations.
func (e *onnxEmbedder) ID() string { return "onnx:" + filepath.Base(filepath.Clean(e.modelDir)) }
func (e *onnxEmbedder) Dim() int   { e.mu.Lock(); defer e.mu.Unlock(); return e.dim }

func (e *onnxEmbedder) Embed(ctx context.Context, texts []string) ([]Vector, error) {
	out := make([]Vector, len(texts))
	for i, t := range texts {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		v, err := e.embedOne(t)
		if err != nil {
			return nil, err
		}
		out[i] = v
	}
	return out, nil
}

func (e *onnxEmbedder) embedOne(text string) (Vector, error) {
	ids := e.tok.encode(text, e.maxLen)
	n := int64(len(ids))
	shape := ort.NewShape(1, n)

	inputIDs := make([]int64, len(ids))
	mask := make([]int64, len(ids))
	types := make([]int64, len(ids))
	for i, id := range ids {
		inputIDs[i] = int64(id)
		mask[i] = 1
	}

	// Build only the inputs the model declares, in the same order as inputNames.
	var inputs []ort.Value
	for _, name := range e.inputNames {
		var data []int64
		switch name {
		case "input_ids":
			data = inputIDs
		case "attention_mask":
			data = mask
		case "token_type_ids":
			data = types
		}
		tensor, err := ort.NewTensor(shape, data)
		if err != nil {
			return nil, err
		}
		defer tensor.Destroy()
		inputs = append(inputs, tensor)
	}

	out := []ort.Value{nil}
	e.mu.Lock()
	err := e.session.Run(inputs, out)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	hidden, ok := out[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output tensor type")
	}
	defer hidden.Destroy()

	dims := hidden.GetShape()
	data := hidden.GetData()

	var pooled []float64
	switch len(dims) {
	case 3: // [1, seq, dim] — mean-pool over real tokens.
		seq, dim := int(dims[1]), int(dims[2])
		pooled = make([]float64, dim)
		count := 0
		for s := 0; s < seq; s++ {
			if s < len(mask) && mask[s] == 0 {
				continue
			}
			count++
			base := s * dim
			for d := 0; d < dim && base+d < len(data); d++ {
				pooled[d] += float64(data[base+d])
			}
		}
		if count == 0 {
			count = 1
		}
		for d := range pooled {
			pooled[d] /= float64(count)
		}
	case 2: // [1, dim] — already pooled.
		dim := int(dims[1])
		pooled = make([]float64, dim)
		for d := 0; d < dim && d < len(data); d++ {
			pooled[d] = float64(data[d])
		}
	default:
		return nil, fmt.Errorf("unexpected output rank %d (shape %v)", len(dims), dims)
	}

	// L2-normalise.
	vec := make(Vector, len(pooled))
	var norm float64
	for d, v := range pooled {
		vec[d] = float32(v)
		norm += v * v
	}
	if norm > 0 {
		inv := float32(1.0 / math.Sqrt(norm))
		for d := range vec {
			vec[d] *= inv
		}
	}

	e.mu.Lock()
	e.dim = len(vec)
	e.mu.Unlock()
	return vec, nil
}

// --- Minimal BERT WordPiece tokenizer. ---

type wordPiece struct {
	vocab    map[string]int
	unkID    int
	clsID    int
	sepID    int
	maxChars int
}

func loadWordPiece(path string) (*wordPiece, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	wp := &wordPiece{vocab: map[string]int{}, maxChars: 100}
	sc := bufio.NewScanner(f)
	idx := 0
	for sc.Scan() {
		tok := strings.TrimRight(sc.Text(), "\r\n")
		wp.vocab[tok] = idx
		idx++
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	get := func(t string, def int) int {
		if id, ok := wp.vocab[t]; ok {
			return id
		}
		return def
	}
	wp.unkID = get("[UNK]", 100)
	wp.clsID = get("[CLS]", 101)
	wp.sepID = get("[SEP]", 102)
	return wp, nil
}

// encode lowercases, splits on whitespace/punctuation, applies greedy
// longest-match WordPiece, and wraps with [CLS]/[SEP], truncating to maxLen.
func (wp *wordPiece) encode(text string, maxLen int) []int {
	ids := []int{wp.clsID}
	for _, word := range basicTokenize(strings.ToLower(text)) {
		if len(word) > wp.maxChars {
			ids = append(ids, wp.unkID)
			continue
		}
		for _, piece := range wp.wordpiece(word) {
			ids = append(ids, piece)
			if len(ids) >= maxLen-1 {
				break
			}
		}
		if len(ids) >= maxLen-1 {
			break
		}
	}
	ids = append(ids, wp.sepID)
	return ids
}

func (wp *wordPiece) wordpiece(word string) []int {
	runes := []rune(word)
	var out []int
	start := 0
	for start < len(runes) {
		end := len(runes)
		var curID = -1
		for end > start {
			sub := string(runes[start:end])
			if start > 0 {
				sub = "##" + sub
			}
			if id, ok := wp.vocab[sub]; ok {
				curID = id
				break
			}
			end--
		}
		if curID == -1 {
			return []int{wp.unkID}
		}
		out = append(out, curID)
		start = end
	}
	return out
}

func basicTokenize(text string) []string {
	var toks []string
	var cur strings.Builder
	flush := func() {
		if cur.Len() > 0 {
			toks = append(toks, cur.String())
			cur.Reset()
		}
	}
	for _, r := range text {
		switch {
		case r == ' ' || r == '\t' || r == '\n' || r == '\r':
			flush()
		case isPunct(r):
			flush()
			toks = append(toks, string(r))
		default:
			cur.WriteRune(r)
		}
	}
	flush()
	return toks
}

func isPunct(r rune) bool {
	return (r >= '!' && r <= '/') || (r >= ':' && r <= '@') ||
		(r >= '[' && r <= '`') || (r >= '{' && r <= '~')
}
