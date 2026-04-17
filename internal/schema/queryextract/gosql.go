package queryextract

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func init() { register(&goExtractor{}) }

// goExtractor catches the common Go database idioms:
//
//   db.Query(`SELECT ...`)
//   db.QueryContext(ctx, "SELECT ...")
//   db.Exec("UPDATE ...")
//   sqlx.Get(db, &out, `SELECT ...`)
//   pgxpool.Query(ctx, `SELECT ...`)
//   dbmap.Select(&out, `SELECT ...`)
//
// It's conservative on purpose — it only catches queries whose FIRST
// string-ish argument is a literal that looks like SQL.
type goExtractor struct{}

func (goExtractor) Lang() string        { return "go" }
func (goExtractor) Match(path string) bool {
	return filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go")
}

// Methods most commonly used for SQL, across database/sql, sqlx, pgx, sqlc,
// gorp, and friends.
var goSQLMethods = []string{
	"Query", "QueryContext", "QueryRow", "QueryRowContext",
	"Exec", "ExecContext",
	"Prepare", "PrepareContext",
	"Select", "SelectContext",
	"Get", "GetContext",
	"NamedExec", "NamedExecContext",
	"NamedQuery", "NamedQueryContext",
	"BeginTx",
}

var reGoCall = regexp.MustCompile(`\b(` + strings.Join(goSQLMethods, "|") + `)\s*\(`)

// Go supports raw string literals with backticks, double-quoted strings,
// and string concatenation with `+`.
func (g goExtractor) Extract(path, content string) []types.QueryRef {
	vars := extractGoSQLVars(content)
	var out []types.QueryRef

	for _, m := range reGoCall.FindAllStringSubmatchIndex(content, -1) {
		start := m[0]
		i := m[1]
		// scan through arguments until we hit a SQL-ish literal, or give up at depth 0.
		for depth := 1; i < len(content) && depth > 0; i++ {
			c := content[i]
			if isGoWS(c) || c == ',' {
				continue
			}
			if c == '(' {
				depth++
				continue
			}
			if c == ')' {
				depth--
				if depth == 0 {
					break
				}
				continue
			}
			// Try to read a string literal.
			if c == '"' || c == '`' {
				lit, _ := readGoStringLiteral(content[i:])
				if lit != "" {
					sql := unquoteGo(lit)
					if looksLikeSQL(sql) {
						out = append(out, types.QueryRef{
							File: path, Line: lineOf(content, start),
							Lang: "go", Kind: "database/sql", SQL: normalizeWS(sql),
						})
					}
				}
				break
			}
			// Try an identifier that might be a sql-holding var.
			tok := firstIdent(content[i:])
			if tok != "" {
				if sql, ok := vars[tok]; ok && looksLikeSQL(sql) {
					out = append(out, types.QueryRef{
						File: path, Line: lineOf(content, start),
						Lang: "go", Kind: "database/sql", SQL: normalizeWS(sql),
					})
				}
				// Could still be ctx or &target — move to next arg.
				// Advance past this token and its comma.
				i += len(tok) - 1
			}
			// Don't keep scanning — we only care about one arg for the heuristic.
			break
		}
	}
	return out
}

var reGoVarAssign = regexp.MustCompile(`(?m)^\s*(?:const\s+|var\s+)?(\w+)\s*(?::?=)\s*(` + "`[^`]*`" + `|"(?:[^"\\]|\\.)*")`)

func extractGoSQLVars(content string) map[string]string {
	out := make(map[string]string)
	for _, m := range reGoVarAssign.FindAllStringSubmatch(content, -1) {
		v := unquoteGo(m[2])
		if looksLikeSQL(v) {
			out[m[1]] = v
		}
	}
	return out
}

func readGoStringLiteral(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	switch s[0] {
	case '`':
		end := strings.IndexByte(s[1:], '`')
		if end < 0 {
			return "", 0
		}
		return s[:end+2], end + 2
	case '"':
		i := 1
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if c == '"' {
				return s[:i+1], i + 1
			}
			if c == '\n' {
				return "", 0
			}
			i++
		}
	}
	return "", 0
}

func unquoteGo(lit string) string {
	if lit == "" {
		return ""
	}
	if lit[0] == '`' {
		return strings.Trim(lit, "`")
	}
	if lit[0] == '"' {
		body := strings.TrimSuffix(strings.TrimPrefix(lit, `"`), `"`)
		var b strings.Builder
		for i := 0; i < len(body); i++ {
			c := body[i]
			if c == '\\' && i+1 < len(body) {
				switch body[i+1] {
				case 'n':
					b.WriteByte('\n')
				case 't':
					b.WriteByte('\t')
				case 'r':
					b.WriteByte('\r')
				case '"':
					b.WriteByte('"')
				case '\\':
					b.WriteByte('\\')
				default:
					b.WriteByte(body[i+1])
				}
				i++
				continue
			}
			b.WriteByte(c)
		}
		return b.String()
	}
	return lit
}

func isGoWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
