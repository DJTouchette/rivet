package semantic

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// httpShape distinguishes the two request/response conventions we support.
type httpShape int

const (
	httpOpenAI httpShape = iota // POST {base}/embeddings  {model,input:[...]} -> {data:[{embedding}]}
	httpOllama                  // POST {base}/api/embeddings {model,prompt}    -> {embedding}
)

// httpEmbedder talks to an embeddings HTTP API. The OpenAI shape covers OpenAI
// itself and any OpenAI-compatible gateway; the Ollama shape covers a local
// Ollama daemon. Both are real network calls — the only difference from a
// bundled model is where the compute runs and whether text leaves the box.
type httpEmbedder struct {
	shape   httpShape
	model   string
	baseURL string
	apiKey  string
	client  *http.Client
	dim     int // learned from the first response
}

func newHTTPEmbedder(shape httpShape, cfg Config) (*httpEmbedder, error) {
	base := strings.TrimRight(cfg.BaseURL, "/")
	model := cfg.Model
	switch shape {
	case httpOpenAI:
		if base == "" {
			base = "https://api.openai.com/v1"
		}
		if model == "" {
			model = "text-embedding-3-small"
		}
	case httpOllama:
		if base == "" {
			base = "http://localhost:11434"
		}
		if model == "" {
			model = "nomic-embed-text"
		}
	}
	return &httpEmbedder{
		shape:   shape,
		model:   model,
		baseURL: base,
		apiKey:  cfg.APIKey,
		client:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (e *httpEmbedder) ID() string {
	// Host is part of identity: the same model name on two providers is not the
	// same vector space. We deliberately exclude the API key.
	return fmt.Sprintf("http:%s:%s", e.baseURL, e.model)
}

func (e *httpEmbedder) Dim() int { return e.dim }

func (e *httpEmbedder) Embed(ctx context.Context, texts []string) ([]Vector, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	switch e.shape {
	case httpOpenAI:
		return e.embedOpenAI(ctx, texts)
	case httpOllama:
		return e.embedOllama(ctx, texts)
	default:
		return nil, ErrUnavailable
	}
}

// --- OpenAI shape: one request embeds the whole batch. ---

type openAIReq struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIResp struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *httpEmbedder) embedOpenAI(ctx context.Context, texts []string) ([]Vector, error) {
	body, err := json.Marshal(openAIReq{Model: e.model, Input: texts})
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/embeddings", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.apiKey)
	}

	raw, err := e.do(req)
	if err != nil {
		return nil, err
	}
	var out openAIResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode embeddings response: %w", err)
	}
	if out.Error != nil {
		return nil, fmt.Errorf("embeddings API error: %s", out.Error.Message)
	}
	if len(out.Data) != len(texts) {
		return nil, fmt.Errorf("embeddings API returned %d vectors for %d inputs", len(out.Data), len(texts))
	}
	vecs := make([]Vector, len(out.Data))
	for i, d := range out.Data {
		vecs[i] = Vector(d.Embedding)
	}
	if len(vecs) > 0 {
		e.dim = len(vecs[0])
	}
	return vecs, nil
}

// --- Ollama shape: one prompt per request. ---

type ollamaReq struct {
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type ollamaResp struct {
	Embedding []float32 `json:"embedding"`
	Error     string    `json:"error"`
}

func (e *httpEmbedder) embedOllama(ctx context.Context, texts []string) ([]Vector, error) {
	vecs := make([]Vector, len(texts))
	for i, t := range texts {
		body, err := json.Marshal(ollamaReq{Model: e.model, Prompt: t})
		if err != nil {
			return nil, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, e.baseURL+"/api/embeddings", bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")

		raw, err := e.do(req)
		if err != nil {
			return nil, err
		}
		var out ollamaResp
		if err := json.Unmarshal(raw, &out); err != nil {
			return nil, fmt.Errorf("decode ollama response: %w", err)
		}
		if out.Error != "" {
			return nil, fmt.Errorf("ollama error: %s", out.Error)
		}
		vecs[i] = Vector(out.Embedding)
	}
	if len(vecs) > 0 {
		e.dim = len(vecs[0])
	}
	return vecs, nil
}

func (e *httpEmbedder) do(req *http.Request) ([]byte, error) {
	resp, err := e.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return nil, fmt.Errorf("embeddings HTTP %d: %s", resp.StatusCode, msg)
	}
	return raw, nil
}
