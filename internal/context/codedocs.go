package context

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// codeDoc mirrors recon's ContextDocInfo JSON (`recon docs` output).
type codeDoc struct {
	File   string `json:"file"`
	Symbol string `json:"symbol"`
	Line   int    `json:"line"`
	Source string `json:"source"` // "comment" or "sidecar"
	Origin string `json:"origin"`
	Body   string `json:"body"`
}

// ReconRunner executes a recon command and returns its captured output —
// satisfied by the embedded recon runner (internal/recon.Run). Defined here so
// package context stays free of the recon dependency.
type ReconRunner func(args []string) (stdout, stderr string, exitCode int, err error)

// LoadCodeDocs loads context docs that live in the code itself —
// rivet:context comments and .context/ sidecar markdown — via recon's docs
// index. Each doc becomes a KindCode Document whose RelatedPaths point at the
// file it annotates, so path queries in recommend land on it. Failures return
// an error; callers degrade gracefully (code docs are an additive tier).
func LoadCodeDocs(run ReconRunner) ([]*Document, error) {
	stdout, stderr, code, err := run([]string{"docs", "--max", "-1"})
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, fmt.Errorf("recon docs failed: %s", strings.TrimSpace(stderr))
	}

	var raw []codeDoc
	if strings.TrimSpace(stdout) == "" || strings.TrimSpace(stdout) == "null" {
		return nil, nil
	}
	if err := json.Unmarshal([]byte(stdout), &raw); err != nil {
		return nil, fmt.Errorf("parse recon docs output: %w", err)
	}

	docs := make([]*Document, 0, len(raw))
	for _, d := range raw {
		name := d.File
		title := d.File
		switch {
		case d.Symbol != "":
			name = d.File + "#" + d.Symbol
			title = d.Symbol + " (" + d.File + ")"
		case d.Source == "comment":
			// File-level comment doc: line-qualify so it can't collide
			// with a sidecar doc on the same file.
			name = fmt.Sprintf("%s:%d", d.File, d.Line)
		}
		if d.Source == "sidecar" {
			title = extractTitle(d.Body, title)
		}
		docs = append(docs, &Document{
			Name:         name,
			Kind:         KindCode,
			Title:        title,
			RelatedPaths: []string{d.File},
			Body:         d.Body,
			RawBody:      d.Body,
			Path:         d.Origin,
		})
	}

	sort.Slice(docs, func(i, j int) bool { return docs[i].Name < docs[j].Name })
	return docs, nil
}
