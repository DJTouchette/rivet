package types

import "testing"

func TestTableQualifiedName(t *testing.T) {
	cases := []struct {
		schema, name, want string
	}{
		{"", "users", "users"},
		{"public", "users", "users"}, // default postgres schema omitted
		{"dbo", "Orders", "Orders"},  // default mssql schema omitted
		{"analytics", "events", "analytics.events"},
	}
	for _, c := range cases {
		tbl := Table{Schema: c.schema, Name: c.name}
		if got := tbl.QualifiedName(); got != c.want {
			t.Errorf("QualifiedName(%q.%q) = %q, want %q", c.schema, c.name, got, c.want)
		}
	}
}
