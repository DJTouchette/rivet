package queryextract

import (
	"regexp"
	"strings"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// looksLikeSQL is a conservative SQL-detection heuristic. It only returns true
// when the text starts with a recognizable SQL verb, optionally preceded by
// common prefixes (comments, CTEs). This cuts down on false positives from
// log lines, error messages, etc.
func looksLikeSQL(s string) bool {
	s = strings.TrimSpace(s)
	if len(s) < 10 {
		return false
	}
	up := strings.ToUpper(s)
	// Skip leading comments.
	for strings.HasPrefix(up, "--") || strings.HasPrefix(up, "/*") {
		if strings.HasPrefix(up, "--") {
			if i := strings.IndexByte(up, '\n'); i >= 0 {
				up = strings.TrimLeft(up[i+1:], " \t\n\r")
				continue
			}
			return false
		}
		if strings.HasPrefix(up, "/*") {
			if i := strings.Index(up, "*/"); i >= 0 {
				up = strings.TrimLeft(up[i+2:], " \t\n\r")
				continue
			}
			return false
		}
	}
	for _, verb := range []string{"SELECT", "INSERT", "UPDATE", "DELETE", "WITH", "MERGE", "UPSERT"} {
		if strings.HasPrefix(up, verb+" ") || strings.HasPrefix(up, verb+"\n") || strings.HasPrefix(up, verb+"\t") {
			return true
		}
	}
	return false
}

// normalizeWS collapses runs of whitespace to a single space.
func normalizeWS(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// ParseShape extracts table and column references along with the clause
// each column appears in (where|join|order_by|group_by|select).
//
// This is heuristic, not a proper parser. It's designed to answer "does the
// query filter on column X of table Y?" — not "is this query valid?".
func ParseShape(q *types.QueryRef) {
	sql := normalizeWS(q.SQL)
	upper := strings.ToUpper(sql)

	// Extract tables from FROM / JOIN / INTO / UPDATE clauses.
	q.Tables = extractTables(sql, upper)

	// Now scan each analyzable clause.
	q.Columns = nil
	q.Columns = append(q.Columns, extractClauseColumns(sql, upper, "WHERE", "where")...)
	q.Columns = append(q.Columns, extractJoinColumns(sql, upper)...)
	q.Columns = append(q.Columns, extractClauseColumns(sql, upper, "ORDER BY", "order_by")...)
	q.Columns = append(q.Columns, extractClauseColumns(sql, upper, "GROUP BY", "group_by")...)
}

var reTableFrom = regexp.MustCompile(`(?i)\b(?:FROM|JOIN|INTO|UPDATE)\s+((?:\[[^\]]+\]|"[^"]+"|\w+)(?:\.(?:\[[^\]]+\]|"[^"]+"|\w+))*)`)

func extractTables(sql, upper string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, m := range reTableFrom.FindAllStringSubmatch(sql, -1) {
		name := normalizeTableName(m[1])
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		out = append(out, name)
	}
	_ = upper
	return out
}

func normalizeTableName(raw string) string {
	// Strip brackets/quotes from each dotted part, keep last component with schema when present.
	parts := strings.Split(raw, ".")
	for i, p := range parts {
		p = strings.TrimSpace(p)
		p = strings.Trim(p, `"`)
		p = strings.TrimPrefix(p, "[")
		p = strings.TrimSuffix(p, "]")
		parts[i] = p
	}
	switch len(parts) {
	case 1:
		return parts[0]
	case 2:
		return parts[0] + "." + parts[1]
	default:
		return strings.Join(parts[len(parts)-2:], ".")
	}
}

// extractClauseColumns finds a clause and parses identifiers inside it until the next keyword.
var reIdent = regexp.MustCompile(`(?:\b|[(,\s])((?:\[[^\]]+\]|"[^"]+"|[A-Za-z_][A-Za-z0-9_]*)(?:\.(?:\[[^\]]+\]|"[^"]+"|[A-Za-z_][A-Za-z0-9_]*))?)`)

var clauseTerminators = []string{
	" ORDER BY ", " GROUP BY ", " HAVING ", " LIMIT ",
	" UNION ", " INTERSECT ", " EXCEPT ",
	" OFFSET ", " FETCH ",
}

func extractClauseColumns(sql, upper, keyword, label string) []types.ColumnRef {
	idx := findClauseStart(upper, keyword)
	if idx < 0 {
		return nil
	}
	idx += len(keyword)
	tail := sql[idx:]
	tailUp := upper[idx:]

	// Trim at the next terminating keyword.
	endIdx := len(tail)
	for _, term := range clauseTerminators {
		if j := strings.Index(tailUp, term); j > 0 && j < endIdx {
			endIdx = j
		}
	}
	segment := tail[:endIdx]

	return collectIdents(segment, label)
}

// findClauseStart scans for a clause at paren-depth 0 to avoid sub-queries.
func findClauseStart(upper, keyword string) int {
	depth := 0
	for i := 0; i < len(upper)-len(keyword); i++ {
		if upper[i] == '(' {
			depth++
		} else if upper[i] == ')' {
			if depth > 0 {
				depth--
			}
		}
		if depth != 0 {
			continue
		}
		if i > 0 && upper[i-1] == ' ' && strings.HasPrefix(upper[i:], keyword) {
			// Require a word boundary after the keyword.
			after := i + len(keyword)
			if after < len(upper) && (upper[after] == ' ' || upper[after] == '\n' || upper[after] == '\t' || upper[after] == '(') {
				return i
			}
		}
	}
	return -1
}

// extractJoinColumns pulls JOIN ... ON conditions specifically.
func extractJoinColumns(sql, upper string) []types.ColumnRef {
	var out []types.ColumnRef
	// Find every " ON " and walk until the next JOIN/WHERE/GROUP/ORDER/etc.
	pos := 0
	for {
		i := strings.Index(upper[pos:], " ON ")
		if i < 0 {
			break
		}
		start := pos + i + 4
		tail := sql[start:]
		tailUp := upper[start:]
		endIdx := len(tail)
		for _, term := range append([]string{" JOIN ", " LEFT ", " RIGHT ", " INNER ", " OUTER ", " WHERE ", " GROUP BY ", " ORDER BY ", " HAVING "}, clauseTerminators...) {
			if j := strings.Index(tailUp, term); j > 0 && j < endIdx {
				endIdx = j
			}
		}
		segment := tail[:endIdx]
		out = append(out, collectIdents(segment, "join")...)
		pos = start + endIdx
	}
	return out
}

// collectIdents extracts bare identifiers from a clause segment, skipping
// obvious non-columns (SQL keywords, numeric literals, string literals).
func collectIdents(segment, label string) []types.ColumnRef {
	// Remove string and numeric literals first to avoid catching them.
	segment = stripSQLLiterals(segment)

	var out []types.ColumnRef
	seen := make(map[string]bool)
	for _, m := range reIdent.FindAllStringSubmatch(segment, -1) {
		id := strings.TrimSpace(m[1])
		if id == "" {
			continue
		}
		if isSQLKeyword(id) {
			continue
		}
		parts := strings.Split(id, ".")
		for i, p := range parts {
			p = strings.Trim(p, `"`)
			p = strings.TrimPrefix(p, "[")
			p = strings.TrimSuffix(p, "]")
			parts[i] = p
		}
		var col types.ColumnRef
		col.Clause = label
		switch len(parts) {
		case 1:
			col.Column = parts[0]
		default:
			col.Table = strings.Join(parts[:len(parts)-1], ".")
			col.Column = parts[len(parts)-1]
		}
		if !isLikelyColumnName(col.Column) {
			continue
		}
		k := col.Table + "|" + col.Column + "|" + col.Clause
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, col)
	}
	return out
}

func isLikelyColumnName(name string) bool {
	if name == "" {
		return false
	}
	// Skip numeric literals.
	for _, r := range name {
		if (r < '0' || r > '9') && r != '.' {
			return true
		}
	}
	return false
}

func stripSQLLiterals(s string) string {
	var b strings.Builder
	i := 0
	for i < len(s) {
		c := s[i]
		if c == '\'' {
			j := i + 1
			for j < len(s) {
				if s[j] == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						j += 2
						continue
					}
					break
				}
				j++
			}
			if j < len(s) {
				b.WriteString(" '?' ")
				i = j + 1
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

var sqlKeywords = map[string]bool{}

func init() {
	words := []string{
		"AND", "OR", "NOT", "NULL", "IS", "IN", "LIKE", "ILIKE", "BETWEEN",
		"EXISTS", "ANY", "ALL", "SOME", "CASE", "WHEN", "THEN", "ELSE", "END",
		"AS", "ASC", "DESC", "NULLS", "FIRST", "LAST",
		"SELECT", "FROM", "JOIN", "LEFT", "RIGHT", "INNER", "OUTER", "FULL",
		"CROSS", "LATERAL", "USING", "ON", "WHERE", "GROUP", "ORDER", "BY",
		"HAVING", "LIMIT", "OFFSET", "FETCH", "UNION", "INTERSECT", "EXCEPT",
		"DISTINCT", "TOP", "INTO", "VALUES", "RETURNING",
		"INSERT", "UPDATE", "DELETE", "SET", "FROM",
		"WITH", "RECURSIVE",
		"TRUE", "FALSE", "UNKNOWN",
		"DATE", "TIMESTAMP", "INTERVAL",
		"COUNT", "SUM", "MIN", "MAX", "AVG", "COALESCE", "NULLIF",
		"CAST", "CONVERT",
	}
	for _, w := range words {
		sqlKeywords[w] = true
	}
}

func isSQLKeyword(s string) bool {
	return sqlKeywords[strings.ToUpper(s)]
}
