package queryextract

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func init() { register(&nodeExtractor{}) }

// nodeExtractor covers JavaScript and TypeScript codebases:
//
//	pool.query("SELECT ...", [args])           // node-postgres / mssql / mysql2
//	pool.query(`SELECT ...`, [args])
//	pool.execute("SELECT ...")
//	await db.query(sql`SELECT ...`)           // tagged template — slonik, postgres.js
//	knex.raw("SELECT ...")
//	prismaClient.$queryRaw`SELECT ...`
//
// Tagged templates don't require the string to be the first arg (`sql`...`
// is the expression). We look for those separately.
type nodeExtractor struct{}

func (nodeExtractor) Lang() string { return "node" }
func (nodeExtractor) Match(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".js", ".mjs", ".cjs", ".ts", ".tsx", ".jsx":
		return true
	}
	return false
}

var nodeSQLMethods = []string{
	"query", "execute", "raw",
	"\\$queryRaw", "\\$executeRaw",
}

var reNodeCall = regexp.MustCompile(`(?:\.|\b)(` + strings.Join(nodeSQLMethods, "|") + `)\s*\(`)

// Tagged templates: `sql\`SELECT ...\“ or `sql.raw\`SELECT ...\“
var reTagged = regexp.MustCompile("\\b(sql|query|prisma\\.\\$queryRaw|db\\.query|psql)\\s*(?:\\.raw)?\\s*`")

var reJSVarAssign = regexp.MustCompile("(?m)^\\s*(?:const|let|var)\\s+(\\w+)\\s*=\\s*(`[^`]*`|\"(?:[^\"\\\\]|\\\\.)*\"|'(?:[^'\\\\]|\\\\.)*')")

func (n nodeExtractor) Extract(path, content string) []types.QueryRef {
	vars := extractJSSQLVars(content)
	var out []types.QueryRef

	// Function-style calls.
	for _, m := range reNodeCall.FindAllStringSubmatchIndex(content, -1) {
		start := m[0]
		i := m[1]
		for i < len(content) && isGoWS(content[i]) {
			i++
		}
		if i >= len(content) {
			continue
		}
		sql, ok := readFirstJSArgSQL(content[i:], vars)
		if !ok || !looksLikeSQL(sql) {
			continue
		}
		kind := "node-sql"
		if strings.HasSuffix(content[m[2]:m[3]], "Raw") {
			kind = "prisma-raw"
		}
		if content[m[2]:m[3]] == "raw" {
			kind = "knex-raw"
		}
		out = append(out, types.QueryRef{
			File: path, Line: lineOf(content, start),
			Lang: "node", Kind: kind, SQL: normalizeWS(sql),
		})
	}

	// Tagged template literals.
	for _, m := range reTagged.FindAllStringSubmatchIndex(content, -1) {
		// m[1] is just past the opening backtick.
		bt := m[1] - 1
		if bt < 0 || bt >= len(content) {
			continue
		}
		close := nextBacktick(content, bt+1)
		if close < 0 {
			continue
		}
		lit := content[bt+1 : close]
		sql := stripTemplateExprs(lit)
		if !looksLikeSQL(sql) {
			continue
		}
		out = append(out, types.QueryRef{
			File: path, Line: lineOf(content, bt),
			Lang: "node", Kind: "tagged-template", SQL: normalizeWS(sql),
		})
	}

	return out
}

func readFirstJSArgSQL(s string, vars map[string]string) (string, bool) {
	if s == "" {
		return "", false
	}
	c := s[0]
	if c == '"' || c == '\'' || c == '`' {
		lit, _ := readJSStringLiteral(s)
		if lit == "" {
			return "", false
		}
		return unquoteJS(lit), true
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

func readJSStringLiteral(s string) (string, int) {
	if s == "" {
		return "", 0
	}
	switch s[0] {
	case '`':
		i := 1
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if c == '`' {
				return s[:i+1], i + 1
			}
			i++
		}
	case '"', '\'':
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

func unquoteJS(lit string) string {
	if lit == "" {
		return ""
	}
	if lit[0] == '`' {
		return stripTemplateExprs(strings.TrimPrefix(strings.TrimSuffix(lit, "`"), "`"))
	}
	if lit[0] == '"' || lit[0] == '\'' {
		body := lit[1 : len(lit)-1]
		return body
	}
	return lit
}

// stripTemplateExprs replaces ${...} with a placeholder so SQL shape parsing
// still works. Nesting is shallow (one level of braces is enough).
func stripTemplateExprs(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '$' && i+1 < len(s) && s[i+1] == '{' {
			depth := 1
			j := i + 2
			for j < len(s) && depth > 0 {
				switch s[j] {
				case '{':
					depth++
				case '}':
					depth--
				}
				j++
			}
			b.WriteString(" ? ")
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func nextBacktick(s string, from int) int {
	for i := from; i < len(s); i++ {
		c := s[i]
		if c == '\\' && i+1 < len(s) {
			i++
			continue
		}
		if c == '`' {
			return i
		}
	}
	return -1
}

func extractJSSQLVars(content string) map[string]string {
	out := make(map[string]string)
	for _, m := range reJSVarAssign.FindAllStringSubmatch(content, -1) {
		v := unquoteJS(m[2])
		if looksLikeSQL(v) {
			out[m[1]] = v
		}
	}
	return out
}
