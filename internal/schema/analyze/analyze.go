// Package analyze turns raw schema + usage + queries into actionable reports:
// unused indexes, redundant indexes, missing indexes, and query coverage.
//
// Everything in here is pure — take inputs, return reports. No I/O, no DB
// connections. That makes the analyzers unit-testable with fixture data.
package analyze

import (
	"fmt"
	"sort"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/queryextract"
	"github.com/djtouchette/rivet/internal/schema/types"
)

// Inputs bundles everything an analysis pass might look at.
type Inputs struct {
	Schema           *types.Schema
	IndexUsage       []types.IndexUsage
	EngineHints      []types.MissingIndexHint
	ExtractedQueries []types.QueryRef
}

// Report collects the full analysis output.
type Report struct {
	Unused    []types.UnusedIndex    `json:"unused,omitempty"`
	Redundant []types.RedundantIndex `json:"redundant,omitempty"`
	Missing   []types.MissingIndex   `json:"missing,omitempty"`
	Coverage  *types.CoverageReport  `json:"coverage,omitempty"`
}

// Run produces all four reports from the given inputs. Each sub-analysis
// degrades gracefully when its specific inputs are missing (e.g. no live
// usage stats → unused section is empty, redundant still runs on schema).
func Run(in Inputs) *Report {
	r := &Report{}
	if in.Schema != nil {
		r.Redundant = DetectRedundant(in.Schema)
	}
	if in.Schema != nil && len(in.IndexUsage) > 0 {
		r.Unused = DetectUnused(in.Schema, in.IndexUsage)
	}
	if in.Schema != nil {
		r.Missing = DetectMissing(in.Schema, in.EngineHints, in.ExtractedQueries)
	}
	if in.Schema != nil && len(in.ExtractedQueries) > 0 {
		r.Coverage = BuildCoverage(in.Schema, in.ExtractedQueries)
	}
	return r
}

// --- Unused ---

// DetectUnused returns indexes whose read counters are essentially zero but
// have write activity (so they still cost us on every write).
//
// Heuristic: reads = scans+seeks+lookups == 0 AND writes > 0.
// Primary keys are always excluded — removing them is almost never what you want.
func DetectUnused(schema *types.Schema, usage []types.IndexUsage) []types.UnusedIndex {
	byIndex := make(map[string]types.IndexUsage, len(usage))
	for _, u := range usage {
		byIndex[key3(u.Schema, u.Table, u.Index)] = u
	}

	var out []types.UnusedIndex
	for _, t := range schema.Tables {
		for _, idx := range t.Indexes {
			if idx.Primary {
				continue
			}
			u, ok := byIndex[key3(t.Schema, t.Name, idx.Name)]
			if !ok {
				continue
			}
			reads := u.Scans + u.Seeks + u.Lookups
			if reads > 0 {
				continue
			}
			if u.Updates == 0 {
				// Paying no reads and no writes — likely a new index or a test DB.
				// Flag as low-confidence.
				out = append(out, types.UnusedIndex{
					Schema: t.Schema, Table: t.Name, Index: idx.Name,
					Reads: reads, Writes: u.Updates, SizeBytes: u.SizeBytes,
					Reason: "no recorded reads or writes — confirm in a production-like environment",
				})
				continue
			}
			out = append(out, types.UnusedIndex{
				Schema: t.Schema, Table: t.Name, Index: idx.Name,
				Reads: reads, Writes: u.Updates, SizeBytes: u.SizeBytes,
				Reason: fmt.Sprintf("zero reads vs %d writes — index is pure write cost", u.Updates),
			})
		}
	}
	sortUnused(out)
	return out
}

// --- Redundant ---

// DetectRedundant flags indexes whose leading columns are a strict prefix of
// another index's leading columns on the same table. The longer index
// covers the shorter one for equality/range lookups — a classic waste.
//
// Exceptions: we never report an index redundant to a unique index unless the
// covering index is also unique, because unique constraints serve dedup as
// well as lookup.
func DetectRedundant(schema *types.Schema) []types.RedundantIndex {
	var out []types.RedundantIndex
	for _, t := range schema.Tables {
		for i := range t.Indexes {
			a := &t.Indexes[i]
			if a.Primary {
				continue
			}
			for j := range t.Indexes {
				if i == j {
					continue
				}
				b := &t.Indexes[j]
				if a.Unique && !b.Unique {
					continue
				}
				if a.Where != b.Where {
					// Partial indexes aren't redundant with full ones.
					continue
				}
				if isPrefix(a.Columns, b.Columns) && len(a.Columns) < len(b.Columns) {
					out = append(out, types.RedundantIndex{
						Schema: t.Schema, Table: t.Name,
						Index: a.Name, CoveredBy: b.Name,
						Reason: fmt.Sprintf("columns (%s) are a prefix of %s(%s)", strings.Join(a.Columns, ","), b.Name, strings.Join(b.Columns, ",")),
					})
					break
				}
				if sameColumns(a.Columns, b.Columns) && a.Name > b.Name {
					// Duplicate column sets — keep one, flag the one with the
					// lexicographically later name.
					out = append(out, types.RedundantIndex{
						Schema: t.Schema, Table: t.Name,
						Index: a.Name, CoveredBy: b.Name,
						Reason: "exact duplicate column set",
					})
					break
				}
			}
		}
	}
	sortRedundant(out)
	return out
}

// --- Missing ---

// DetectMissing combines engine hints and code-analysis candidates.
//
// Engine hints (MSSQL DMVs, pg_qualstats) are high-confidence because they
// come from the real optimizer observing real workloads.
//
// Code candidates come from predicates we see in the source: columns used in
// WHERE / JOIN / ORDER BY that aren't covered by any existing index on the
// referenced table.
func DetectMissing(schema *types.Schema, hints []types.MissingIndexHint, queries []types.QueryRef) []types.MissingIndex {
	byTable := make(map[string]types.Table, len(schema.Tables))
	for _, t := range schema.Tables {
		byTable[strings.ToLower(t.QualifiedName())] = t
		byTable[strings.ToLower(t.Name)] = t
	}

	var out []types.MissingIndex

	// Engine hints → structured candidates.
	for _, h := range hints {
		cols := append([]string(nil), h.EqualityColumns...)
		cols = append(cols, h.InequalityColumns...)
		if len(cols) == 0 {
			continue
		}
		out = append(out, types.MissingIndex{
			Schema: h.Schema, Table: h.Table,
			Columns: cols, Include: h.IncludedColumns,
			Confidence: "high",
			Source:     h.Source,
			Evidence:   []string{fmt.Sprintf("engine reports impact score %.1f", h.Impact)},
		})
	}

	// Parse query shapes and find candidates.
	type candidateKey struct {
		schema, table, cols string
	}
	candidates := make(map[candidateKey]*types.MissingIndex)

	for i := range queries {
		q := &queries[i]
		// Copy so we don't mutate the caller's slice element.
		local := *q
		ParseShapeCompat(&local)

		for _, refTable := range local.Tables {
			t, ok := byTable[strings.ToLower(refTable)]
			if !ok {
				continue
			}
			// Collect columns this query uses on THIS table.
			cols := make([]string, 0, 4)
			seen := make(map[string]bool)
			for _, c := range local.Columns {
				if c.Table != "" && !strings.EqualFold(c.Table, refTable) && !strings.HasSuffix(strings.ToLower(refTable), strings.ToLower(c.Table)) {
					continue
				}
				if c.Column == "" {
					continue
				}
				if !columnExists(t, c.Column) {
					continue
				}
				if seen[c.Column] {
					continue
				}
				seen[c.Column] = true
				cols = append(cols, c.Column)
			}
			if len(cols) == 0 {
				continue
			}
			if isCovered(t, cols) {
				continue
			}

			k := candidateKey{t.Schema, t.Name, strings.Join(cols, ",")}
			existing, ok := candidates[k]
			if !ok {
				existing = &types.MissingIndex{
					Schema: t.Schema, Table: t.Name,
					Columns:    cols,
					Confidence: "medium",
					Source:     "code-analysis",
					Evidence:   []string{"predicate appears in application code without a covering index"},
				}
				candidates[k] = existing
			}
			if len(existing.SampleQueries) < 3 {
				existing.SampleQueries = append(existing.SampleQueries, *q)
			}
		}
	}

	for _, c := range candidates {
		out = append(out, *c)
	}

	// Combine duplicates from engine + code sources into a single "combined" entry.
	out = combineMissing(out)

	sortMissing(out)
	return out
}

// --- Coverage ---

// BuildCoverage produces a per-table coverage report from extracted queries.
func BuildCoverage(schema *types.Schema, queries []types.QueryRef) *types.CoverageReport {
	tableHits := make(map[string]*types.TableCoverage)
	predHits := make(map[string]map[string]*types.PredicateHit) // tableKey → predKey → hit

	byTable := make(map[string]*types.Table)
	for i := range schema.Tables {
		t := &schema.Tables[i]
		byTable[strings.ToLower(t.QualifiedName())] = t
		byTable[strings.ToLower(t.Name)] = t
	}

	for i := range queries {
		q := &queries[i]
		local := *q
		ParseShapeCompat(&local)

		for _, refTable := range local.Tables {
			t := byTable[strings.ToLower(refTable)]
			if t == nil {
				continue
			}
			tk := t.Schema + "." + t.Name
			cov, ok := tableHits[tk]
			if !ok {
				var names []string
				for _, idx := range t.Indexes {
					names = append(names, idx.Name)
				}
				cov = &types.TableCoverage{
					Schema: t.Schema, Table: t.Name,
					Indexes: names,
				}
				tableHits[tk] = cov
				predHits[tk] = make(map[string]*types.PredicateHit)
			}
			cov.QueriesHit++

			// Group columns by clause.
			colsByClause := map[string][]string{}
			for _, c := range local.Columns {
				if !columnExists(*t, c.Column) {
					continue
				}
				if c.Table != "" && !strings.EqualFold(c.Table, refTable) && !strings.HasSuffix(strings.ToLower(refTable), strings.ToLower(c.Table)) {
					continue
				}
				colsByClause[c.Clause] = append(colsByClause[c.Clause], c.Column)
			}
			for clause, cols := range colsByClause {
				cols = dedup(cols)
				sort.Strings(cols)
				k := clause + "|" + strings.Join(cols, ",")
				ph, ok := predHits[tk][k]
				if !ok {
					covered, covBy := coverage(*t, cols)
					ph = &types.PredicateHit{
						Columns: cols, Clause: clause,
						Covered: covered, CoveringIndex: covBy,
					}
					predHits[tk][k] = ph
				}
				ph.Occurrences++
			}
		}
	}

	var keys []string
	for k := range tableHits {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	report := &types.CoverageReport{}
	for _, k := range keys {
		cov := tableHits[k]
		for _, ph := range predHits[k] {
			cov.Predicates = append(cov.Predicates, *ph)
		}
		sort.Slice(cov.Predicates, func(i, j int) bool {
			return cov.Predicates[i].Clause+strings.Join(cov.Predicates[i].Columns, ",") <
				cov.Predicates[j].Clause+strings.Join(cov.Predicates[j].Columns, ",")
		})
		report.Tables = append(report.Tables, *cov)
	}
	return report
}

// --- helpers ---

// ParseShapeCompat enriches a QueryRef with Tables/Columns derived from its SQL.
// Exposed as a variable so tests can substitute a pre-parsed no-op.
var ParseShapeCompat = queryextract.ParseShape

func columnExists(t types.Table, name string) bool {
	for _, c := range t.Columns {
		if strings.EqualFold(c.Name, name) {
			return true
		}
	}
	return false
}

func isCovered(t types.Table, cols []string) bool {
	if len(cols) == 0 {
		return true
	}
	for _, idx := range t.Indexes {
		if indexCovers(idx, cols) {
			return true
		}
	}
	return false
}

func coverage(t types.Table, cols []string) (bool, string) {
	for _, idx := range t.Indexes {
		if indexCovers(idx, cols) {
			return true, idx.Name
		}
	}
	return false, ""
}

// indexCovers returns true when idx's leading columns include all of cols
// (order-insensitive for equality predicates).
func indexCovers(idx types.Index, cols []string) bool {
	if len(idx.Columns) < len(cols) {
		return false
	}
	need := make(map[string]bool, len(cols))
	for _, c := range cols {
		need[strings.ToLower(c)] = true
	}
	leading := idx.Columns
	if len(leading) > len(cols) {
		leading = leading[:len(cols)]
	}
	for _, c := range leading {
		if !need[strings.ToLower(c)] {
			return false
		}
	}
	return len(leading) == len(cols)
}

func isPrefix(a, b []string) bool {
	if len(a) > len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func sameColumns(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if !strings.EqualFold(a[i], b[i]) {
			return false
		}
	}
	return true
}

func combineMissing(in []types.MissingIndex) []types.MissingIndex {
	type k struct {
		schema, table, cols string
	}
	grouped := make(map[k]*types.MissingIndex)
	for i := range in {
		c := in[i]
		key := k{strings.ToLower(c.Schema), strings.ToLower(c.Table), strings.ToLower(strings.Join(c.Columns, ","))}
		ex, ok := grouped[key]
		if !ok {
			copied := c
			grouped[key] = &copied
			continue
		}
		// Merge: if two sources agree, confidence is high.
		ex.Source = "combined"
		ex.Confidence = "high"
		ex.Evidence = append(ex.Evidence, c.Evidence...)
		if len(ex.Include) == 0 {
			ex.Include = c.Include
		}
		ex.SampleQueries = append(ex.SampleQueries, c.SampleQueries...)
	}

	out := make([]types.MissingIndex, 0, len(grouped))
	for _, v := range grouped {
		out = append(out, *v)
	}
	return out
}

func dedup(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := in[:0]
	for _, s := range in {
		lo := strings.ToLower(s)
		if seen[lo] {
			continue
		}
		seen[lo] = true
		out = append(out, s)
	}
	return out
}

func key3(a, b, c string) string { return a + "." + b + "." + c }

func sortUnused(u []types.UnusedIndex) {
	sort.Slice(u, func(i, j int) bool {
		if u[i].Writes != u[j].Writes {
			return u[i].Writes > u[j].Writes
		}
		return u[i].Index < u[j].Index
	})
}

func sortRedundant(u []types.RedundantIndex) {
	sort.Slice(u, func(i, j int) bool {
		if u[i].Table != u[j].Table {
			return u[i].Table < u[j].Table
		}
		return u[i].Index < u[j].Index
	})
}

func sortMissing(u []types.MissingIndex) {
	sort.Slice(u, func(i, j int) bool {
		pri := func(c string) int {
			switch c {
			case "high":
				return 0
			case "medium":
				return 1
			default:
				return 2
			}
		}
		pi, pj := pri(u[i].Confidence), pri(u[j].Confidence)
		if pi != pj {
			return pi < pj
		}
		if u[i].Table != u[j].Table {
			return u[i].Table < u[j].Table
		}
		return strings.Join(u[i].Columns, ",") < strings.Join(u[j].Columns, ",")
	})
}
