package semantic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHTTPEmbedderOpenAI(t *testing.T) {
	var gotAuth, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		var req openAIReq
		body, _ := io.ReadAll(r.Body)
		json.Unmarshal(body, &req)

		resp := openAIResp{}
		for range req.Input {
			resp.Data = append(resp.Data, struct {
				Embedding []float32 `json:"embedding"`
			}{Embedding: []float32{0.1, 0.2, 0.3}})
		}
		json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	e, _ := newHTTPEmbedder(httpOpenAI, Config{Model: "text-embedding-3-small", BaseURL: srv.URL, APIKey: "sk-test"})
	vecs, err := e.Embed(context.Background(), []string{"a", "b"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if len(vecs) != 2 || len(vecs[0]) != 3 || vecs[0][1] != 0.2 {
		t.Errorf("vecs = %v", vecs)
	}
	if e.Dim() != 3 {
		t.Errorf("dim learned = %d, want 3", e.Dim())
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if gotPath != "/embeddings" {
		t.Errorf("path = %q, want /embeddings", gotPath)
	}
}

func TestHTTPEmbedderOpenAIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		io.WriteString(w, `{"error":{"message":"bad key"}}`)
	}))
	defer srv.Close()

	e, _ := newHTTPEmbedder(httpOpenAI, Config{BaseURL: srv.URL})
	if _, err := e.Embed(context.Background(), []string{"x"}); err == nil {
		t.Error("expected error on HTTP 401")
	}
}

func TestHTTPEmbedderOllama(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if !strings.HasSuffix(r.URL.Path, "/api/embeddings") {
			t.Errorf("ollama path = %q", r.URL.Path)
		}
		json.NewEncoder(w).Encode(ollamaResp{Embedding: []float32{1, 0, 0, 0}})
	}))
	defer srv.Close()

	e, _ := newHTTPEmbedder(httpOllama, Config{Model: "nomic-embed-text", BaseURL: srv.URL})
	vecs, err := e.Embed(context.Background(), []string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	// Ollama embeds one prompt per request.
	if calls != 3 || len(vecs) != 3 {
		t.Errorf("calls=%d vecs=%d, want 3/3", calls, len(vecs))
	}
}
