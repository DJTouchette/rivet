package queryextract

import (
	"strings"
	"testing"

	"github.com/djtouchette/rivet/internal/schema/types"
)

// --- Go (database/sql, sqlx, pgx) ---

func TestGo_DoubleQuoted(t *testing.T) {
	src := `package repo
func (r *Repo) Get(db *sql.DB) {
	rows, _ := db.Query("SELECT id, email FROM users WHERE tenant_id = $1", 1)
	_ = rows
}`
	refs := goExtractor{}.Extract("repo.go", src)
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if refs[0].Lang != "go" {
		t.Errorf("lang = %q, want go", refs[0].Lang)
	}
	if !strings.Contains(refs[0].SQL, "FROM users") {
		t.Errorf("SQL = %q", refs[0].SQL)
	}
}

func TestGo_BacktickLiteral(t *testing.T) {
	src := "package repo\n" +
		"func f(db *sqlx.DB) {\n" +
		"	db.Query(`SELECT name FROM accounts WHERE active = true`)\n" +
		"}\n"
	refs := goExtractor{}.Extract("repo.go", src)
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if !strings.Contains(refs[0].SQL, "FROM accounts") {
		t.Errorf("SQL = %q", refs[0].SQL)
	}
}

// --- Python (psycopg / sqlalchemy) ---

func TestPython_Execute(t *testing.T) {
	src := "def get(cur):\n" +
		"    cur.execute(\"SELECT id FROM orders WHERE status = %s\", (s,))\n"
	refs := pyExtractor{}.Extract("q.py", src)
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if refs[0].Lang != "python" || refs[0].Kind != "psycopg" {
		t.Errorf("lang/kind = %s/%s, want python/psycopg", refs[0].Lang, refs[0].Kind)
	}
	if !strings.Contains(refs[0].SQL, "FROM orders") {
		t.Errorf("SQL = %q", refs[0].SQL)
	}
}

func TestPython_SqlalchemyText(t *testing.T) {
	src := "q = text(\"SELECT count(*) FROM events\")\n"
	refs := pyExtractor{}.Extract("q.py", src)
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if refs[0].Kind != "sqlalchemy" {
		t.Errorf("kind = %q, want sqlalchemy", refs[0].Kind)
	}
}

// --- Node (pg / knex / tagged templates) ---

func TestNode_QueryCall(t *testing.T) {
	src := "async function f(pool) {\n" +
		"  const r = await pool.query('SELECT id FROM accounts WHERE email = $1', [e]);\n" +
		"}\n"
	refs := nodeExtractor{}.Extract("q.ts", src)
	if len(refs) != 1 {
		t.Fatalf("refs = %d, want 1", len(refs))
	}
	if refs[0].Lang != "node" {
		t.Errorf("lang = %q, want node", refs[0].Lang)
	}
	if !strings.Contains(refs[0].SQL, "FROM accounts") {
		t.Errorf("SQL = %q", refs[0].SQL)
	}
}

func TestNode_TaggedTemplate(t *testing.T) {
	src := "const rows = await sql`SELECT id, total FROM invoices WHERE paid = false`;\n"
	refs := nodeExtractor{}.Extract("q.js", src)
	if len(refs) == 0 {
		t.Fatal("expected a ref from the tagged template")
	}
	if !strings.Contains(refs[0].SQL, "FROM invoices") {
		t.Errorf("SQL = %q", refs[0].SQL)
	}
}

// --- non-SQL strings must be ignored ---

func TestExtractors_IgnoreNonSQL(t *testing.T) {
	if refs := (goExtractor{}).Extract("x.go", `db.Query("not a query, just text")`); len(refs) != 0 {
		t.Errorf("go: captured non-SQL string: %+v", refs)
	}
	if refs := (pyExtractor{}).Extract("x.py", `cur.execute("hello world")`); len(refs) != 0 {
		t.Errorf("python: captured non-SQL string: %+v", refs)
	}
}

// --- sqlparse: pure SQL-shape helpers ---

func TestLooksLikeSQL(t *testing.T) {
	yes := []string{
		"SELECT id FROM users",
		"insert into t (a) values (1)",
		"UPDATE orders SET x = 1",
		"delete from logs where ts < now()",
	}
	for _, s := range yes {
		if !looksLikeSQL(s) {
			t.Errorf("looksLikeSQL(%q) = false, want true", s)
		}
	}
	no := []string{"hello world", "just a label", "click here", ""}
	for _, s := range no {
		if looksLikeSQL(s) {
			t.Errorf("looksLikeSQL(%q) = true, want false", s)
		}
	}
}

func TestIsSQLKeyword(t *testing.T) {
	for _, k := range []string{"select", "FROM", "Where", "join"} {
		if !isSQLKeyword(k) {
			t.Errorf("isSQLKeyword(%q) = false, want true", k)
		}
	}
	for _, k := range []string{"email", "users", "tenant_id"} {
		if isSQLKeyword(k) {
			t.Errorf("isSQLKeyword(%q) = true, want false", k)
		}
	}
}

func TestParseShape_TablesAndColumns(t *testing.T) {
	q := &types.QueryRef{
		SQL: "SELECT u.id, u.email FROM users u JOIN orders o ON o.user_id = u.id WHERE u.email = $1 ORDER BY u.created_at",
	}
	ParseShape(q)

	hasTable := func(name string) bool {
		for _, tbl := range q.Tables {
			if strings.Contains(strings.ToLower(tbl), name) {
				return true
			}
		}
		return false
	}
	if !hasTable("users") || !hasTable("orders") {
		t.Errorf("tables = %v, want users + orders", q.Tables)
	}

	colInClause := func(col, clause string) bool {
		for _, c := range q.Columns {
			if strings.EqualFold(c.Column, col) && c.Clause == clause {
				return true
			}
		}
		return false
	}
	if !colInClause("email", "where") {
		t.Errorf("columns = %+v, want email in where clause", q.Columns)
	}
}
