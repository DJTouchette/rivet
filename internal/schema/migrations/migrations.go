// Package migrations reconstructs a schema from on-disk SQL migration files.
//
// This is a pragmatic extractor, not a general-purpose SQL parser. It handles
// the subset that covers 95% of real-world migration files: CREATE TABLE with
// columns, primary keys, and inline foreign keys; CREATE INDEX (and CREATE
// UNIQUE INDEX) with optional INCLUDE and WHERE clauses; ALTER TABLE ADD
// COLUMN / ADD CONSTRAINT; and DROP TABLE / DROP INDEX.
//
// When it sees something it doesn't understand, it records the filename in
// Unparsed and moves on instead of failing.
package migrations

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// Options tunes migration discovery.
type Options struct {
	// Dialect hints at the engine family — "postgres" or "mssql".
	// Used only for minor syntax differences; parsing is permissive.
	Dialect string

	// DefaultSchema is the implicit schema for unqualified tables.
	// "public" for postgres, "dbo" for mssql. Defaults to "public".
	DefaultSchema string
}

// Result is the reconstructed static schema plus metadata.
type Result struct {
	Schema   *types.Schema
	Summary  types.MigrationsSummary
	Unparsed []string
}

// Parse reads all SQL files under dir (recursively) in lexical order
// and accumulates a synthetic schema.
func Parse(dir string, opts Options) (*Result, error) {
	if opts.DefaultSchema == "" {
		if opts.Dialect == "mssql" {
			opts.DefaultSchema = "dbo"
		} else {
			opts.DefaultSchema = "public"
		}
	}

	files, err := discover(dir)
	if err != nil {
		return nil, err
	}

	sch := &types.Schema{
		Source: "migrations",
	}
	switch opts.Dialect {
	case "mssql":
		sch.Engine = types.EngineMSSQL
	case "postgres":
		sch.Engine = types.EnginePostgres
	default:
		sch.Engine = types.EngineUnknown
	}

	p := &parser{
		defaultSchema: opts.DefaultSchema,
		tables:        make(map[string]*types.Table),
	}

	for _, f := range files {
		content, err := os.ReadFile(f)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", f, err)
		}
		p.file = filepath.Base(f)
		if err := p.parse(string(content)); err != nil {
			p.unparsed = append(p.unparsed, p.file)
		}
	}

	// Flatten tables into slice (sorted for deterministic output).
	names := make([]string, 0, len(p.tables))
	for k := range p.tables {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, k := range names {
		sch.Tables = append(sch.Tables, *p.tables[k])
	}

	totalIndexes := 0
	for _, t := range sch.Tables {
		totalIndexes += len(t.Indexes)
	}

	return &Result{
		Schema: sch,
		Summary: types.MigrationsSummary{
			Directory: dir,
			Files:     len(files),
			Tables:    len(sch.Tables),
			Indexes:   totalIndexes,
			Dialect:   opts.Dialect,
			Unparsed:  p.unparsed,
		},
		Unparsed: p.unparsed,
	}, nil
}

// discover walks dir and returns .sql paths in lexical order.
func discover(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		if strings.HasSuffix(strings.ToLower(path), ".sql") {
			files = append(files, path)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("walking %s: %w", dir, err)
	}
	sort.Strings(files)
	return files, nil
}

// --- parser ---

type parser struct {
	defaultSchema string
	tables        map[string]*types.Table
	file          string
	unparsed      []string
}

// Statement regexps — all case-insensitive. We first strip comments and
// normalize whitespace, then look at the start of each statement.
var (
	reLineComment  = regexp.MustCompile(`--[^\n]*`)
	reBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	reWS           = regexp.MustCompile(`\s+`)

	reCreateTable = regexp.MustCompile(`(?is)^\s*CREATE\s+TABLE\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_."\[\]]+)\s*\((.*)\)\s*$`)
	reCreateIndex = regexp.MustCompile(`(?is)^\s*CREATE\s+(UNIQUE\s+)?(?:CLUSTERED\s+|NONCLUSTERED\s+)?INDEX\s+(?:IF\s+NOT\s+EXISTS\s+)?([a-zA-Z0-9_."\[\]]+)\s+ON\s+([a-zA-Z0-9_."\[\]]+)\s*(?:USING\s+(\w+)\s*)?\(([^)]+)\)(.*)$`)
	reDropTable   = regexp.MustCompile(`(?is)^\s*DROP\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-zA-Z0-9_."\[\]]+)`)
	reDropIndex   = regexp.MustCompile(`(?is)^\s*DROP\s+INDEX\s+(?:IF\s+EXISTS\s+)?(?:[a-zA-Z0-9_."\[\]]+\.)?([a-zA-Z0-9_."\[\]]+)`)
	reAlterTable  = regexp.MustCompile(`(?is)^\s*ALTER\s+TABLE\s+(?:IF\s+EXISTS\s+)?([a-zA-Z0-9_."\[\]]+)\s+(.*)$`)

	reInclude = regexp.MustCompile(`(?is)INCLUDE\s*\(([^)]+)\)`)
	reWhere   = regexp.MustCompile(`(?is)WHERE\s+(.+?)\s*;?\s*$`)
)

func (p *parser) parse(content string) error {
	content = reBlockComment.ReplaceAllString(content, " ")
	content = reLineComment.ReplaceAllString(content, " ")

	for _, stmt := range splitStatements(content) {
		trimmed := strings.TrimSpace(stmt)
		if trimmed == "" {
			continue
		}
		upper := strings.ToUpper(trimmed)
		switch {
		case strings.HasPrefix(upper, "CREATE TABLE"),
			strings.HasPrefix(upper, "CREATE GLOBAL TEMPORARY TABLE"):
			p.parseCreateTable(trimmed)
		case isCreateIndexStmt(upper):
			p.parseCreateIndex(trimmed)
		case strings.HasPrefix(upper, "DROP TABLE"):
			p.parseDropTable(trimmed)
		case strings.HasPrefix(upper, "DROP INDEX"):
			p.parseDropIndex(trimmed)
		case strings.HasPrefix(upper, "ALTER TABLE"):
			p.parseAlterTable(trimmed)
		}
	}
	return nil
}

func matchPrefix(s, prefix string) bool {
	up := strings.ToUpper(s)
	return strings.HasPrefix(up, prefix)
}

// isCreateIndexStmt recognises the CREATE INDEX variants we parse:
//   CREATE INDEX, CREATE UNIQUE INDEX, CREATE CLUSTERED INDEX,
//   CREATE NONCLUSTERED INDEX, CREATE UNIQUE NONCLUSTERED INDEX, etc.
func isCreateIndexStmt(upper string) bool {
	if !strings.HasPrefix(upper, "CREATE ") {
		return false
	}
	tail := strings.TrimSpace(upper[len("CREATE "):])
	// Strip any combination of UNIQUE/CLUSTERED/NONCLUSTERED in any order.
	for _, kw := range []string{"UNIQUE ", "CLUSTERED ", "NONCLUSTERED "} {
		for strings.HasPrefix(tail, kw) {
			tail = strings.TrimSpace(tail[len(kw):])
		}
	}
	return strings.HasPrefix(tail, "INDEX ")
}

// splitStatements is a depth-aware splitter on ';' that ignores semicolons
// inside parens, string literals, and dollar-quoted bodies.
func splitStatements(s string) []string {
	var out []string
	depth := 0
	inSingle := false
	inDouble := false
	dollarTag := ""
	start := 0

	for i := 0; i < len(s); i++ {
		c := s[i]

		if dollarTag != "" {
			if strings.HasPrefix(s[i:], dollarTag) {
				i += len(dollarTag) - 1
				dollarTag = ""
			}
			continue
		}
		if inSingle {
			if c == '\'' {
				if i+1 < len(s) && s[i+1] == '\'' {
					i++ // escaped
				} else {
					inSingle = false
				}
			}
			continue
		}
		if inDouble {
			if c == '"' {
				inDouble = false
			}
			continue
		}

		switch c {
		case '\'':
			inSingle = true
		case '"':
			inDouble = true
		case '$':
			// Detect $tag$ or $$ block quote
			end := strings.IndexByte(s[i+1:], '$')
			if end >= 0 {
				dollarTag = s[i : i+1+end+1]
			}
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ';':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}

func (p *parser) parseCreateTable(stmt string) {
	m := reCreateTable.FindStringSubmatch(stmt)
	if m == nil {
		p.unparsed = append(p.unparsed, p.file)
		return
	}
	schema, name := splitQualified(m[1], p.defaultSchema)
	body := m[2]

	t := p.getOrCreateTable(schema, name)
	t.Columns = nil // redefine
	t.PrimaryKey = nil

	items := splitTopLevelCommas(body)
	position := 1
	for _, item := range items {
		item = strings.TrimSpace(item)
		upper := strings.ToUpper(item)

		switch {
		case strings.HasPrefix(upper, "PRIMARY KEY"):
			t.PrimaryKey = extractColList(item)
		case strings.HasPrefix(upper, "CONSTRAINT") && strings.Contains(upper, "PRIMARY KEY"):
			t.PrimaryKey = extractColList(item)
		case strings.HasPrefix(upper, "UNIQUE"):
			t.Indexes = append(t.Indexes, types.Index{
				Name:    fmt.Sprintf("%s_unique_%d", t.Name, len(t.Indexes)+1),
				Columns: extractColList(item),
				Unique:  true,
			})
		case strings.HasPrefix(upper, "FOREIGN KEY") || (strings.HasPrefix(upper, "CONSTRAINT") && strings.Contains(upper, "FOREIGN KEY")):
			if fk, ok := parseInlineFK(item, p.defaultSchema); ok {
				t.ForeignKeys = append(t.ForeignKeys, fk)
			}
		case strings.HasPrefix(upper, "CHECK"), strings.HasPrefix(upper, "CONSTRAINT"):
			// skip — we don't model checks
		default:
			col, pk, ok := parseColumnDef(item)
			if !ok {
				continue
			}
			col.Position = position
			position++
			t.Columns = append(t.Columns, col)
			if pk {
				t.PrimaryKey = append(t.PrimaryKey, col.Name)
			}
		}
	}
}

func (p *parser) parseCreateIndex(stmt string) {
	m := reCreateIndex.FindStringSubmatch(stmt)
	if m == nil {
		p.unparsed = append(p.unparsed, p.file)
		return
	}
	unique := strings.TrimSpace(m[1]) != ""
	name := stripIdent(m[2])
	schema, table := splitQualified(m[3], p.defaultSchema)
	method := m[4]
	cols := splitColExprList(m[5])
	tail := m[6]

	idx := types.Index{
		Name:    name,
		Columns: cols,
		Unique:  unique,
		Method:  strings.ToLower(method),
	}
	if im := reInclude.FindStringSubmatch(tail); im != nil {
		idx.Include = splitColExprList(im[1])
	}
	if wm := reWhere.FindStringSubmatch(tail); wm != nil {
		idx.Where = strings.TrimSpace(wm[1])
	}

	t := p.getOrCreateTable(schema, table)
	t.Indexes = append(t.Indexes, idx)
}

func (p *parser) parseDropTable(stmt string) {
	m := reDropTable.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	schema, name := splitQualified(m[1], p.defaultSchema)
	delete(p.tables, schema+"."+name)
}

func (p *parser) parseDropIndex(stmt string) {
	m := reDropIndex.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	target := stripIdent(m[1])
	for _, t := range p.tables {
		out := t.Indexes[:0]
		for _, idx := range t.Indexes {
			if idx.Name != target {
				out = append(out, idx)
			}
		}
		t.Indexes = out
	}
}

func (p *parser) parseAlterTable(stmt string) {
	m := reAlterTable.FindStringSubmatch(stmt)
	if m == nil {
		return
	}
	schema, name := splitQualified(m[1], p.defaultSchema)
	t := p.getOrCreateTable(schema, name)

	rest := strings.TrimSpace(m[2])
	// Support a single ADD COLUMN / DROP COLUMN / RENAME COLUMN / ADD CONSTRAINT per statement.
	up := strings.ToUpper(rest)

	switch {
	case strings.HasPrefix(up, "ADD COLUMN"):
		colSpec := strings.TrimSpace(rest[len("ADD COLUMN"):])
		if col, _, ok := parseColumnDef(colSpec); ok {
			col.Position = len(t.Columns) + 1
			t.Columns = append(t.Columns, col)
		}
	case strings.HasPrefix(up, "ADD "):
		// Could be ADD PRIMARY KEY, ADD CONSTRAINT, ADD FOREIGN KEY …
		tail := strings.TrimSpace(rest[4:])
		tailUp := strings.ToUpper(tail)
		switch {
		case strings.HasPrefix(tailUp, "PRIMARY KEY"):
			t.PrimaryKey = extractColList(tail)
		case strings.HasPrefix(tailUp, "FOREIGN KEY"), strings.HasPrefix(tailUp, "CONSTRAINT"):
			if fk, ok := parseInlineFK(tail, p.defaultSchema); ok {
				t.ForeignKeys = append(t.ForeignKeys, fk)
			}
		}
	case strings.HasPrefix(up, "DROP COLUMN"):
		colName := stripIdent(firstToken(rest[len("DROP COLUMN"):]))
		out := t.Columns[:0]
		for _, c := range t.Columns {
			if c.Name != colName {
				out = append(out, c)
			}
		}
		t.Columns = out
	case strings.HasPrefix(up, "RENAME COLUMN"):
		tail := strings.TrimSpace(rest[len("RENAME COLUMN"):])
		fields := strings.Fields(tail)
		if len(fields) >= 3 && strings.EqualFold(fields[1], "TO") {
			from := stripIdent(fields[0])
			to := stripIdent(fields[2])
			for i := range t.Columns {
				if t.Columns[i].Name == from {
					t.Columns[i].Name = to
				}
			}
		}
	}
}

func (p *parser) getOrCreateTable(schema, name string) *types.Table {
	k := schema + "." + name
	if t, ok := p.tables[k]; ok {
		return t
	}
	t := &types.Table{Schema: schema, Name: name}
	p.tables[k] = t
	return t
}

// --- small helpers ---

// splitTopLevelCommas splits on ',' at depth 0 (paren-aware).
func splitTopLevelCommas(s string) []string {
	var out []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case ',':
			if depth == 0 {
				out = append(out, s[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, s[start:])
	return out
}

// splitQualified takes "schema.name" or "name" and returns (schema, name)
// using defaultSchema when unqualified. Brackets, quotes, and backticks are stripped.
func splitQualified(raw, defaultSchema string) (string, string) {
	raw = strings.TrimSpace(raw)
	parts := splitByDotRespectingQuotes(raw)
	for i, p := range parts {
		parts[i] = stripIdent(p)
	}
	if len(parts) == 1 {
		return defaultSchema, parts[0]
	}
	return parts[0], parts[len(parts)-1]
}

func splitByDotRespectingQuotes(s string) []string {
	var parts []string
	start := 0
	inQuote := byte(0)
	for i := 0; i < len(s); i++ {
		c := s[i]
		if inQuote != 0 {
			if c == inQuote {
				inQuote = 0
			}
			continue
		}
		switch c {
		case '"', '`':
			inQuote = c
		case '[':
			inQuote = ']'
		case '.':
			parts = append(parts, s[start:i])
			start = i + 1
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func stripIdent(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, `"`)
	s = strings.Trim(s, "`")
	s = strings.TrimPrefix(s, "[")
	s = strings.TrimSuffix(s, "]")
	return s
}

// extractColList extracts "(col1, col2)" from things like
//   "PRIMARY KEY (col1, col2)" or "UNIQUE (email)".
func extractColList(s string) []string {
	open := strings.Index(s, "(")
	if open < 0 {
		return nil
	}
	close := strings.LastIndex(s, ")")
	if close <= open {
		return nil
	}
	return splitColExprList(s[open+1 : close])
}

func splitColExprList(inner string) []string {
	parts := splitTopLevelCommas(inner)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		// Drop trailing direction / NULLS ordering.
		up := strings.ToUpper(p)
		for _, suffix := range []string{" ASC", " DESC", " NULLS FIRST", " NULLS LAST"} {
			if strings.HasSuffix(up, suffix) {
				p = strings.TrimSpace(p[:len(p)-len(suffix)])
				up = strings.ToUpper(p)
			}
		}
		out = append(out, stripIdent(p))
	}
	return out
}

// parseColumnDef parses a single column definition line within a CREATE TABLE.
// Returns (column, inlinePrimaryKey, ok).
func parseColumnDef(spec string) (types.Column, bool, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return types.Column{}, false, false
	}
	// First token is the column name.
	name, rest := splitFirstToken(spec)
	if name == "" {
		return types.Column{}, false, false
	}
	name = stripIdent(name)
	if !isIdentFirstChar(name[0]) {
		return types.Column{}, false, false
	}

	// Next token(s) form the data type — could be "VARCHAR(255)" or "DOUBLE PRECISION".
	dataType, rest := extractDataType(rest)

	upper := strings.ToUpper(rest)

	col := types.Column{
		Name:     name,
		DataType: dataType,
		Nullable: !strings.Contains(upper, "NOT NULL"),
	}

	// DEFAULT … — grab the next token or parenthesized expression.
	if idx := strings.Index(upper, "DEFAULT"); idx >= 0 {
		tail := strings.TrimSpace(rest[idx+len("DEFAULT"):])
		col.Default = firstExpr(tail)
	}

	inlinePK := strings.Contains(upper, "PRIMARY KEY")
	return col, inlinePK, true
}

func extractDataType(s string) (string, string) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", ""
	}
	// Fast path: word[(len)] then whitespace.
	// Slow path: "DOUBLE PRECISION", "TIMESTAMP WITH TIME ZONE".
	first, rest := splitFirstToken(s)
	base := strings.ToUpper(stripIdent(first))
	switch base {
	case "DOUBLE":
		if strings.HasPrefix(strings.ToUpper(strings.TrimSpace(rest)), "PRECISION") {
			_, rest2 := splitFirstToken(rest)
			return "double precision", rest2
		}
	case "CHARACTER":
		nxt, rest2 := splitFirstToken(rest)
		if strings.ToUpper(stripIdent(nxt)) == "VARYING" {
			n, rest3 := consumeParenOpt(rest2)
			return strings.TrimSpace("character varying" + n), rest3
		}
	case "TIMESTAMP":
		nxt := strings.ToUpper(strings.TrimSpace(rest))
		if strings.HasPrefix(nxt, "WITH") || strings.HasPrefix(nxt, "WITHOUT") {
			// Consume up to "ZONE" for WITH TIME ZONE / WITHOUT TIME ZONE.
			idx := strings.Index(strings.ToUpper(rest), "ZONE")
			if idx >= 0 {
				combined := strings.ToLower("TIMESTAMP " + strings.TrimSpace(rest[:idx+len("ZONE")]))
				return combined, rest[idx+len("ZONE"):]
			}
		}
	}
	// Include (N) or (N, M) if present.
	n, rest2 := consumeParenOpt(rest)
	return strings.ToLower(first + n), rest2
}

func consumeParenOpt(s string) (string, string) {
	s = strings.TrimLeft(s, " \t\n\r")
	if !strings.HasPrefix(s, "(") {
		return "", s
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '(' {
			depth++
		} else if s[i] == ')' {
			depth--
			if depth == 0 {
				return s[:i+1], s[i+1:]
			}
		}
	}
	return "", s
}

func splitFirstToken(s string) (string, string) {
	s = strings.TrimLeft(s, " \t\n\r")
	for i, r := range s {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' || r == ',' || r == '(' {
			return s[:i], s[i:]
		}
	}
	return s, ""
}

func firstExpr(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if s[0] == '\'' {
		end := strings.IndexByte(s[1:], '\'')
		if end >= 0 {
			return s[:end+2]
		}
		return s
	}
	if s[0] == '(' {
		depth := 0
		for i := 0; i < len(s); i++ {
			if s[i] == '(' {
				depth++
			} else if s[i] == ')' {
				depth--
				if depth == 0 {
					return s[:i+1]
				}
			}
		}
	}
	tok, _ := splitFirstToken(s)
	return tok
}

func firstToken(s string) string {
	t, _ := splitFirstToken(s)
	return t
}

func isIdentFirstChar(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z')
}

// parseInlineFK handles definitions like
//   FOREIGN KEY (col) REFERENCES other.table (othercol) ON DELETE CASCADE
//   CONSTRAINT name FOREIGN KEY (col) REFERENCES …
func parseInlineFK(s, defaultSchema string) (types.ForeignKey, bool) {
	up := strings.ToUpper(s)
	fkIdx := strings.Index(up, "FOREIGN KEY")
	if fkIdx < 0 {
		return types.ForeignKey{}, false
	}
	refIdx := strings.Index(up, "REFERENCES")
	if refIdx < 0 {
		return types.ForeignKey{}, false
	}
	cols := extractColList(s[fkIdx:refIdx])

	afterRef := strings.TrimSpace(s[refIdx+len("REFERENCES"):])
	tableTok, tail := splitFirstToken(afterRef)
	refSchema, refTable := splitQualified(tableTok, defaultSchema)

	var refCols []string
	if len(tail) > 0 && strings.TrimSpace(tail)[0] == '(' {
		refCols = extractColList(tail)
	}

	fk := types.ForeignKey{
		Columns:          cols,
		ReferencedSchema: refSchema,
		ReferencedTable:  refTable,
		ReferencedColumns: refCols,
	}

	tailUp := strings.ToUpper(tail)
	if idx := strings.Index(tailUp, "ON DELETE"); idx >= 0 {
		fk.OnDelete = extractAction(tail[idx+len("ON DELETE"):])
	}
	if idx := strings.Index(tailUp, "ON UPDATE"); idx >= 0 {
		fk.OnUpdate = extractAction(tail[idx+len("ON UPDATE"):])
	}
	return fk, true
}

func extractAction(s string) string {
	s = strings.TrimSpace(s)
	up := strings.ToUpper(s)
	switch {
	case strings.HasPrefix(up, "CASCADE"):
		return "CASCADE"
	case strings.HasPrefix(up, "SET NULL"):
		return "SET NULL"
	case strings.HasPrefix(up, "SET DEFAULT"):
		return "SET DEFAULT"
	case strings.HasPrefix(up, "RESTRICT"):
		return "RESTRICT"
	case strings.HasPrefix(up, "NO ACTION"):
		return "NO ACTION"
	}
	return ""
}

// Ensure unused symbols don't cause errors in strict builds.
var _ = reWS
