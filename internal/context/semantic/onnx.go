//go:build onnx

// This file is the real bundled-model embedder. It is compiled only with
// `-tags onnx` and depends on:
//
//   - github.com/yalue/onnxruntime_go  (go get it; needs libonnxruntime present)
//   - a model directory (Config.Model) containing:
//     model.onnx  — a sentence-embedding model exporting input ids/mask and a
//     token-level last_hidden_state output (e.g. bge-small-en-v1.5)
//     vocab.txt   — the WordPiece vocabulary, one token per line
//
// It produces mean-pooled, L2-normalised sentence embeddings — the standard
// recipe for BERT-family encoders like bge/gte/e5. It was authored against the
// onnxruntime_go API but has NOT been executed in the dev sandbox (no ORT
// library / model file there); treat the first real run as the validation step.
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

type onnxEmbedder struct {
	modelDir string
	tok      *wordPiece
	maxLen   int

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
		if err := ort.InitializeEnvironment(); err != nil {
			return nil, fmt.Errorf("%w: init onnxruntime: %v", ErrUnavailable, err)
		}
	}
	sess, err := ort.NewDynamicAdvancedSession(
		filepath.Join(cfg.Model, "model.onnx"),
		[]string{"input_ids", "attention_mask", "token_type_ids"},
		[]string{"last_hidden_state"},
		nil,
	)
	if err != nil {
		return nil, fmt.Errorf("%w: load model: %v", ErrUnavailable, err)
	}
	return &onnxEmbedder{modelDir: cfg.Model, tok: tok, maxLen: 256, session: sess}, nil
}

func (e *onnxEmbedder) ID() string { return "onnx:" + filepath.Clean(e.modelDir) }
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

	inputIDs := make([]int64, len(ids))
	mask := make([]int64, len(ids))
	types := make([]int64, len(ids))
	for i, id := range ids {
		inputIDs[i] = int64(id)
		mask[i] = 1
	}

	shape := ort.NewShape(1, n)
	tIDs, err := ort.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, err
	}
	defer tIDs.Destroy()
	tMask, err := ort.NewTensor(shape, mask)
	if err != nil {
		return nil, err
	}
	defer tMask.Destroy()
	tTypes, err := ort.NewTensor(shape, types)
	if err != nil {
		return nil, err
	}
	defer tTypes.Destroy()

	out := []ort.Value{nil}
	e.mu.Lock()
	err = e.session.Run([]ort.Value{tIDs, tMask, tTypes}, out)
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	hidden, ok := out[0].(*ort.Tensor[float32])
	if !ok {
		return nil, fmt.Errorf("unexpected output tensor type")
	}
	defer hidden.Destroy()

	// last_hidden_state shape: [1, seq, dim]. Mean-pool over real tokens.
	dims := hidden.GetShape()
	if len(dims) != 3 {
		return nil, fmt.Errorf("expected 3-D hidden state, got %v", dims)
	}
	seq, dim := int(dims[1]), int(dims[2])
	data := hidden.GetData()

	pooled := make([]float64, dim)
	count := 0
	for s := 0; s < seq; s++ {
		if mask[s] == 0 {
			continue
		}
		count++
		base := s * dim
		for d := 0; d < dim; d++ {
			pooled[d] += float64(data[base+d])
		}
	}
	if count == 0 {
		count = 1
	}
	vec := make(Vector, dim)
	var norm float64
	for d := 0; d < dim; d++ {
		v := pooled[d] / float64(count)
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
	e.dim = dim
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
