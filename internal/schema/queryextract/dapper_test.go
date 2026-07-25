package queryextract

import (
	"strings"
	"testing"
)

func TestDapper_PlainLiteral(t *testing.T) {
	src := `
		public class Repo {
		    public void M(IDbConnection conn) {
		        var rows = conn.Query<User>("SELECT id, email FROM users WHERE tenant_id = @t", new { t = 1 });
		    }
		}`
	refs := (dapperExtractor{}).Extract("Repo.cs", src)
	if len(refs) != 1 {
		t.Fatalf("refs: %d", len(refs))
	}
	if refs[0].Kind != "dapper" || refs[0].Lang != "csharp" {
		t.Errorf("lang/kind: got %s/%s", refs[0].Lang, refs[0].Kind)
	}
	if !strings.Contains(refs[0].SQL, "tenant_id = @t") {
		t.Errorf("SQL: %q", refs[0].SQL)
	}
}

func TestDapper_VerbatimString(t *testing.T) {
	src := `
		conn.Execute(@"
		    UPDATE orders
		    SET status = 'closed'
		    WHERE id = @id
		", new { id = 42 });`
	refs := (dapperExtractor{}).Extract("x.cs", src)
	if len(refs) != 1 {
		t.Fatalf("refs: %d", len(refs))
	}
	if !strings.Contains(refs[0].SQL, "UPDATE orders") {
		t.Errorf("got %q", refs[0].SQL)
	}
}

func TestDapper_RawStringLiteral(t *testing.T) {
	src := "var q = conn.QueryAsync<Row>(\"\"\"\n    SELECT id FROM accounts WHERE email = @e\n\"\"\", new { e });"
	refs := (dapperExtractor{}).Extract("x.cs", src)
	if len(refs) != 1 {
		t.Fatalf("refs: %d\n%s", len(refs), src)
	}
}

func TestDapper_VariableRef(t *testing.T) {
	src := `
		private const string Sql = "SELECT * FROM users WHERE id = @id";

		public User Get(IDbConnection conn, int id) {
		    return conn.QueryFirstOrDefault<User>(Sql, new { id });
		}`
	refs := (dapperExtractor{}).Extract("x.cs", src)
	if len(refs) != 1 {
		t.Fatalf("refs: %d", len(refs))
	}
	if !strings.Contains(refs[0].SQL, "WHERE id = @id") {
		t.Errorf("expected variable-resolved SQL, got %q", refs[0].SQL)
	}
}

func TestDapper_IgnoresNonSQL(t *testing.T) {
	src := `
		var log = "Error connecting";
		logger.LogInformation(log);
		Console.WriteLine("not a query");`
	refs := (dapperExtractor{}).Extract("x.cs", src)
	if len(refs) != 0 {
		t.Fatalf("expected no refs, got %d: %v", len(refs), refs)
	}
}

func TestDapper_Concatenation(t *testing.T) {
	src := `
		conn.Query<Row>("SELECT id FROM users WHERE " + "tenant_id = @t AND deleted = 0", new { t = 1 });`
	refs := (dapperExtractor{}).Extract("x.cs", src)
	if len(refs) != 1 {
		t.Fatalf("refs: %d", len(refs))
	}
	if !strings.Contains(refs[0].SQL, "tenant_id") {
		t.Errorf("concatenation lost: %q", refs[0].SQL)
	}
}

func TestDapper_GenericTypeParam(t *testing.T) {
	src := `conn.Query<Dictionary<string, object>>("SELECT * FROM t WHERE x = @x", new { x });`
	refs := (dapperExtractor{}).Extract("x.cs", src)
	if len(refs) != 1 {
		t.Fatalf("refs: %d", len(refs))
	}
}
