package queryextract

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

func init() { register(&dapperExtractor{}) }

// dapperExtractor extracts SQL from C# source files that use Dapper.
// Patterns it handles:
//
//	connection.Query<T>("SELECT …", …)
//	connection.Execute("UPDATE …", …)
//	connection.QueryAsync<T>("SELECT …", …)
//	connection.QueryFirst/QueryFirstOrDefault/QuerySingle/QuerySingleOrDefault
//	connection.ExecuteScalar<T>(…)
//	var sql = "SELECT …";  connection.Query<T>(sql, …)
//
// It recognises:
//   - raw string literals: "..."
//   - verbatim strings:    @"..."
//   - raw string literals: """ ... """  (C# 11)
//
// And walks string concatenation with + across the same statement.
type dapperExtractor struct{}

func (dapperExtractor) Lang() string { return "csharp" }

func (dapperExtractor) Match(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".cs" || ext == ".cshtml"
}

// Dapper method names we track.
var dapperMethods = []string{
	"Query", "QueryAsync",
	"QueryFirst", "QueryFirstAsync",
	"QueryFirstOrDefault", "QueryFirstOrDefaultAsync",
	"QuerySingle", "QuerySingleAsync",
	"QuerySingleOrDefault", "QuerySingleOrDefaultAsync",
	"QueryMultiple", "QueryMultipleAsync",
	"Execute", "ExecuteAsync",
	"ExecuteScalar", "ExecuteScalarAsync",
	"ExecuteReader", "ExecuteReaderAsync",
}

// reDapperCall matches a Dapper-style method invocation, capturing the method
// name and leaving the cursor at the opening paren.
//
// The generic-parameter group allows up to two levels of nesting, which
// handles cases like Query<Dictionary<string, object>>(...).
var reDapperCall = regexp.MustCompile(`\b(` + strings.Join(dapperMethods, "|") + `)\s*(?:<(?:[^<>]|<[^<>]*>){0,300}>)?\s*\(`)

// reVarAssign matches "var x = "SELECT ..."" and "string x = "SELECT ...""
// so we can resolve variable-holding queries.
var reVarAssign = regexp.MustCompile(`(?m)^\s*(?:var|string|const\s+string|private\s+const\s+string|public\s+const\s+string|static\s+readonly\s+string|private\s+static\s+readonly\s+string|internal\s+static\s+readonly\s+string)\s+(\w+)\s*=\s*(@?"(?:[^"\\]|\\.)*"|"""[\s\S]*?""")`)

func (d dapperExtractor) Extract(path, content string) []types.QueryRef {
	vars := extractSQLVars(content)
	var out []types.QueryRef

	// Find every Dapper call site.
	for _, m := range reDapperCall.FindAllStringSubmatchIndex(content, -1) {
		start := m[0]
		openParen := m[1] // index AFTER the match (so `(` is at m[1]-1? no — reDapperCall ends at `(` so m[1] is past the `(`)
		// Walk past the open paren to the first argument.
		argStart := openParen
		if argStart > len(content) {
			continue
		}
		// Peel to the first non-whitespace char.
		i := argStart
		for i < len(content) && isCSharpWS(content[i]) {
			i++
		}
		if i >= len(content) {
			continue
		}
		sql, ok := readFirstArgSQL(content[i:], vars)
		if !ok || !looksLikeSQL(sql) {
			continue
		}
		out = append(out, types.QueryRef{
			File: path,
			Line: lineOf(content, start),
			Lang: "csharp",
			Kind: "dapper",
			SQL:  normalizeWS(sql),
		})
	}

	return out
}

// extractSQLVars pulls out `var/string/const NAME = "SQL..."` assignments.
func extractSQLVars(content string) map[string]string {
	vars := make(map[string]string)
	for _, m := range reVarAssign.FindAllStringSubmatch(content, -1) {
		name := m[1]
		literal := m[2]
		sql := unquoteCSharp(literal)
		if sql != "" && looksLikeSQL(sql) {
			vars[name] = sql
		}
	}
	return vars
}

// readFirstArgSQL reads the first argument of a Dapper method call starting at
// index 0 of s. It returns the extracted SQL, or ("", false) if it couldn't
// find one.
func readFirstArgSQL(s string, vars map[string]string) (string, bool) {
	if len(s) == 0 {
		return "", false
	}

	// Case 1: string literal (possibly verbatim or raw).
	if s[0] == '"' || (s[0] == '@' && len(s) > 1 && s[1] == '"') || strings.HasPrefix(s, `"""`) {
		literal, rest := readStringLiteral(s)
		if literal == "" {
			return "", false
		}
		sql := unquoteCSharp(literal)
		// Handle string concatenation with '+'.
		rest = strings.TrimLeft(rest, " \t\n\r")
		for strings.HasPrefix(rest, "+") {
			rest = strings.TrimLeft(rest[1:], " \t\n\r")
			if rest == "" {
				break
			}
			if rest[0] == '"' || strings.HasPrefix(rest, `@"`) || strings.HasPrefix(rest, `"""`) {
				more, tail := readStringLiteral(rest)
				if more == "" {
					break
				}
				sql += unquoteCSharp(more)
				rest = strings.TrimLeft(tail, " \t\n\r")
				continue
			}
			// Variable reference mid-concatenation — replace with placeholder so
			// the SQL shape remains recognizable.
			tok := firstIdent(rest)
			if tok == "" {
				break
			}
			if v, ok := vars[tok]; ok {
				sql += v
			} else {
				sql += " /*?*/ "
			}
			rest = strings.TrimLeft(strings.TrimPrefix(rest, tok), " \t\n\r")
		}
		return sql, true
	}

	// Case 2: bare identifier (variable reference).
	tok := firstIdent(s)
	if tok == "" {
		return "", false
	}
	if sql, ok := vars[tok]; ok {
		return sql, true
	}
	return "", false
}

// readStringLiteral reads a C# string literal ("..."|@"..."|""" ... """),
// returning (literal including delimiters, rest of input). Returns "" if
// nothing was found.
func readStringLiteral(s string) (string, string) {
	if strings.HasPrefix(s, `"""`) {
		// C# 11 raw string literal. Terminator is three+ double-quotes.
		// Find the closing sequence.
		end := strings.Index(s[3:], `"""`)
		if end < 0 {
			return "", s
		}
		end += 3
		// Advance over any additional closing quotes.
		for end+1 < len(s) && s[end+1] == '"' {
			end++
		}
		return s[:end+3], s[end+3:]
	}
	if strings.HasPrefix(s, `@"`) {
		// Verbatim string: only "" counts as escape, other chars literal.
		i := 2
		for i < len(s) {
			if s[i] == '"' {
				if i+1 < len(s) && s[i+1] == '"' {
					i += 2
					continue
				}
				return s[:i+1], s[i+1:]
			}
			i++
		}
		return "", s
	}
	if s[0] == '"' {
		i := 1
		for i < len(s) {
			c := s[i]
			if c == '\\' && i+1 < len(s) {
				i += 2
				continue
			}
			if c == '"' {
				return s[:i+1], s[i+1:]
			}
			if c == '\n' {
				return "", s // unterminated string — bail
			}
			i++
		}
	}
	return "", s
}

// unquoteCSharp strips delimiters and normalizes escape sequences in a C#
// string literal. Good enough for SQL extraction — we don't need exact parity
// with csc.
func unquoteCSharp(lit string) string {
	if strings.HasPrefix(lit, `"""`) {
		lit = strings.TrimPrefix(lit, `"""`)
		lit = strings.TrimSuffix(lit, `"""`)
		return lit
	}
	if strings.HasPrefix(lit, `@"`) {
		lit = strings.TrimPrefix(lit, `@"`)
		lit = strings.TrimSuffix(lit, `"`)
		return strings.ReplaceAll(lit, `""`, `"`)
	}
	if strings.HasPrefix(lit, `"`) {
		lit = strings.TrimPrefix(lit, `"`)
		lit = strings.TrimSuffix(lit, `"`)
		// Minimal unescaping: \n \t \" \\
		out := strings.Builder{}
		for i := 0; i < len(lit); i++ {
			c := lit[i]
			if c == '\\' && i+1 < len(lit) {
				switch lit[i+1] {
				case 'n':
					out.WriteByte('\n')
					i++
					continue
				case 't':
					out.WriteByte('\t')
					i++
					continue
				case 'r':
					out.WriteByte('\r')
					i++
					continue
				case '"':
					out.WriteByte('"')
					i++
					continue
				case '\\':
					out.WriteByte('\\')
					i++
					continue
				}
			}
			out.WriteByte(c)
		}
		return out.String()
	}
	return lit
}

func firstIdent(s string) string {
	i := 0
	for i < len(s) && isIdentByte(s[i]) {
		i++
	}
	return s[:i]
}

func isIdentByte(b byte) bool {
	return b == '_' || (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9')
}

func isCSharpWS(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}
