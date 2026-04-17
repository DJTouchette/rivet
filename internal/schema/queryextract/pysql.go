package queryextract

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func init() { register(&pyExtractor{}) }

// pyExtractor catches:
//   cursor.execute("SELECT ...", ...)
//   conn.execute(sa.text("SELECT ..."))
//   session.execute(text("SELECT ..."))
//   await conn.fetch("SELECT ...")           # asyncpg
//   engine.connect().execute("SELECT ...")
//   sql = """SELECT ..."""  cursor.execute(sql, ...)
type pyExtractor struct{}

func (pyExtractor) Lang() string        { return "python" }
func (pyExtractor) Match(path string) bool { return filepath.Ext(path) == ".py" }

var pySQLMethods = []string{
	"execute", "executemany", "executescript",
	"fetch", "fetchrow", "fetchval",
	"text", // sqlalchemy text()
}

var rePyCall = regexp.MustCompile(`(?:\.|\b)(` + strings.Join(pySQLMethods, "|") + `)\s*\(`)

var rePyVarAssign = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*(?:r|R|rb|Rb|rB|RB)?("""[\s\S]*?"""|'''[\s\S]*?'''|"(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')`)

func (p pyExtractor) Extract(path, content string) []types.QueryRef {
	vars := extractPySQLVars(content)
	var out []types.QueryRef

	for _, m := range rePyCall.FindAllStringSubmatchIndex(content, -1) {
		start := m[0]
		i := m[1]
		// Skip whitespace.
		for i < len(content) && isGoWS(content[i]) {
			i++
		}
		if i >= len(content) {
			continue
		}
		sql, ok := readFirstPyArgSQL(content[i:], vars)
		if !ok || !looksLikeSQL(sql) {
			continue
		}
		kind := "psycopg"
		if content[m[2]:m[3]] == "text" || content[m[2]:m[3]] == "fetch" || content[m[2]:m[3]] == "fetchrow" {
			kind = "sqlalchemy"
		}
		out = append(out, types.QueryRef{
			File: path, Line: lineOf(content, start),
			Lang: "python", Kind: kind, SQL: normalizeWS(sql),
		})
	}
	return out
}

func readFirstPyArgSQL(s string, vars map[string]string) (string, bool) {
	if s == "" {
		return "", false
	}
	c := s[0]
	if c == '"' || c == '\'' || strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, `'''`) {
		lit, _ := readPyStringLiteral(s)
		return unquotePy(lit), lit != ""
	}
	// Strip "r" / "R" / "rb" raw prefix.
	if c == 'r' || c == 'R' {
		if len(s) > 1 && (s[1] == '"' || s[1] == '\'') {
			lit, _ := readPyStringLiteral(s[1:])
			return unquotePy(lit), lit != ""
		}
	}
	tok := firstIdent(s)
	if tok == "" {
		return "", false
	}
	if v, ok := vars[tok]; ok {
		return v, true
	}
	return "", false
}

func readPyStringLiteral(s string) (string, int) {
	if strings.HasPrefix(s, `"""`) || strings.HasPrefix(s, `'''`) {
		delim := s[:3]
		end := strings.Index(s[3:], delim)
		if end < 0 {
			return "", 0
		}
		return s[:3+end+3], 3 + end + 3
	}
	if s[0] == '"' || s[0] == '\'' {
		delim := s[0]
		i := 1
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if c == delim {
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

func unquotePy(lit string) string {
	if lit == "" {
		return ""
	}
	if strings.HasPrefix(lit, `"""`) || strings.HasPrefix(lit, `'''`) {
		return lit[3 : len(lit)-3]
	}
	if lit[0] == '"' || lit[0] == '\'' {
		body := lit[1 : len(lit)-1]
		// Minimal escape handling is sufficient.
		return body
	}
	return lit
}

func extractPySQLVars(content string) map[string]string {
	out := make(map[string]string)
	for _, m := range rePyVarAssign.FindAllStringSubmatch(content, -1) {
		v := unquotePy(m[2])
		if looksLikeSQL(v) {
			out[m[1]] = v
		}
	}
	return out
}
