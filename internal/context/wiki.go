package context

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"golang.org/x/sync/errgroup"
)

// Default on-disk locations, relative to the project root.
const (
	WikiDir     = ".rivet/wiki"
	RunbooksDir = ".rivet/runbooks"
	// DraftsSubdir holds agent-drafted runbooks awaiting human promotion. It is
	// excluded from the loaded/retrievable set.
	DraftsSubdir = "drafts"
)

// LoadWiki loads free-form reference docs (KindWiki) from the native
// .rivet/wiki/ tree plus any extra roots in wikiPaths (e.g. a checked-out Azure
// DevOps wiki — "../project.wiki/**" — or a "docs/**" tree). Trees are walked
// recursively; a missing root is skipped, not an error.
//
// Each wikiPath may be a directory or a glob ending in /** or /*; in both cases
// the directory before the wildcard is walked for *.md. Azure DevOps wiki
// artifacts (.order files, the .attachments/ folder) are skipped.
//
// Trust boundary: wikiPaths comes from .rivet/config.yaml, a trusted,
// version-controlled project file (the same file can declare arbitrary shell
// capabilities). Roots may deliberately point outside the project — a sibling
// ADO wiki checkout is the canonical case — so paths are NOT confined to the
// repo. Only files ending in .md are ever read; nothing is executed.
func LoadWiki(projectRoot string, wikiPaths []string) ([]*Document, error) {
	roots := append([]string{WikiDir}, wikiPaths...)

	var docs []*Document
	seen := map[string]bool{} // dedupe by absolute path across overlapping roots
	for _, root := range roots {
		dir := globRoot(root)
		full := dir
		if !filepath.IsAbs(full) {
			full = filepath.Join(projectRoot, dir)
		}
		walked, err := walkDocs(full, KindWiki, seen)
		if err != nil {
			return nil, err
		}
		docs = append(docs, walked...)
	}
	sortDocs(docs)
	return docs, nil
}

// LoadRunbooks loads actionable procedures (KindRunbook) from .rivet/runbooks/,
// recursively, excluding the drafts/ staging area and archive/. Triggers,
// severity, and last_tested frontmatter are parsed. A missing dir is not an error.
func LoadRunbooks(projectRoot string) ([]*Document, error) {
	full := RunbooksDir
	if !filepath.IsAbs(full) {
		full = filepath.Join(projectRoot, RunbooksDir)
	}
	docs, err := walkDocs(full, KindRunbook, map[string]bool{})
	if err != nil {
		return nil, err
	}
	sortDocs(docs)
	return docs, nil
}

// globRoot strips a trailing /** or /* (or bare ** / *) from a configured path,
// returning the directory to walk.
func globRoot(p string) string {
	p = strings.TrimSuffix(p, "/**")
	p = strings.TrimSuffix(p, "/*")
	if p == "**" || p == "*" || p == "" {
		return "."
	}
	return p
}

// walkDocs recursively reads *.md files under dir into Documents of the given
// kind. It skips hidden dirs, ADO wiki artifacts, and the drafts/ + archive/
// staging dirs. Names are the slash-relative path (minus .md) within dir, so
// nested pages stay unique. A missing dir yields no docs and no error.
func walkDocs(dir string, kind Kind, seen map[string]bool) ([]*Document, error) {
	info, err := os.Stat(dir)
	if os.IsNotExist(err) || (err == nil && !info.IsDir()) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dir, err)
	}

	// Phase 1: walk the tree (cheap, sequential) to enumerate the files to read.
	// Dedup runs here while single-threaded, so the seen map needs no lock.
	type job struct{ path, name string }
	var jobs []job
	err = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (strings.HasPrefix(name, ".") || name == DraftsSubdir || name == "archive" || name == ".attachments") {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") || strings.HasPrefix(d.Name(), ".") {
			return nil // skips .order and other non-markdown
		}
		abs, _ := filepath.Abs(path)
		if seen[abs] {
			return nil
		}
		seen[abs] = true

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = filepath.Base(path)
		}
		name := strings.TrimSuffix(filepath.ToSlash(rel), ".md")
		jobs = append(jobs, job{path: path, name: name})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", dir, err)
	}

	// Phase 2: read + parse files concurrently — the I/O-bound part. Each worker
	// owns its own slot in docs, so the result slice needs no lock, order is
	// preserved, and the first read error aborts the group.
	docs := make([]*Document, len(jobs))
	g := new(errgroup.Group)
	g.SetLimit(runtime.GOMAXPROCS(0))
	for i, j := range jobs {
		g.Go(func() error {
			doc, readErr := readDoc(j.path, j.name, kind)
			if readErr != nil {
				return readErr
			}
			docs[i] = doc
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return nil, fmt.Errorf("walking %s: %w", dir, err)
	}
	return docs, nil
}

// readDoc reads a single markdown file into a Document of the given kind,
// parsing frontmatter (including runbook-specific fields).
func readDoc(path, name string, kind Kind) (*Document, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}
	raw := string(body)
	fm, content := parseFrontmatter(raw)
	title := extractTitle(content, name)

	doc := &Document{
		Name:         name,
		Kind:         kind,
		Title:        title,
		Tags:         fm.tags,
		RelatedPaths: fm.relatedPaths,
		Owner:        fm.owner,
		Body:         content,
		RawBody:      raw,
		Path:         path,
		Triggers:     fm.triggers,
		Severity:     fm.severity,
	}
	if fm.lastReviewed != "" {
		if t, err := time.Parse("2006-01-02", fm.lastReviewed); err == nil {
			doc.LastReviewed = t
		}
	}
	if fm.lastTested != "" {
		if t, err := time.Parse("2006-01-02", fm.lastTested); err == nil {
			doc.LastTested = t
		}
	}
	return doc, nil
}

func sortDocs(docs []*Document) {
	sort.Slice(docs, func(i, j int) bool {
		if docs[i].Kind != docs[j].Kind {
			return docs[i].Kind < docs[j].Kind
		}
		return docs[i].Name < docs[j].Name
	})
}
