package mssql

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestIndexUsage(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("dm_db_index_usage_stats").WillReturnRows(
		sqlmock.NewRows([]string{"schema", "table", "index", "seeks", "scans", "lookups", "updates", "size"}).
			AddRow("dbo", "Orders", "PK_Orders", 500, 10, 3, 200, 65536).
			AddRow("dbo", "Orders", "IX_Dead", 0, 0, 0, 4000, 32768),
	)

	d := &driver{db: db, schema: "dbo"}
	usage, err := d.IndexUsage(context.Background())
	if err != nil {
		t.Fatalf("IndexUsage: %v", err)
	}
	if len(usage) != 2 {
		t.Fatalf("got %d, want 2", len(usage))
	}
	if usage[0].Index != "PK_Orders" || usage[0].Seeks != 500 || usage[0].Updates != 200 {
		t.Errorf("row 0 = %+v", usage[0])
	}
	// The dead index: no reads, only write cost.
	if usage[1].Seeks != 0 || usage[1].Scans != 0 || usage[1].Lookups != 0 || usage[1].Updates != 4000 {
		t.Errorf("row 1 = %+v, want all reads 0 and updates 4000", usage[1])
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}

func TestLoadTables(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	mock.ExpectQuery("FROM sys.tables").WillReturnRows(
		sqlmock.NewRows([]string{"schema", "table", "rows", "size"}).
			AddRow("dbo", "Orders", 1200, 131072).
			AddRow("sales", "Invoices", 90, 16384),
	)

	d := &driver{db: db, schema: "dbo"}
	tables, err := d.loadTables(context.Background())
	if err != nil {
		t.Fatalf("loadTables: %v", err)
	}
	if len(tables) != 2 {
		t.Fatalf("got %d tables, want 2", len(tables))
	}
	orders := tables[key("dbo", "Orders")]
	if orders == nil || orders.RowEstimate != 1200 || orders.SizeBytes != 131072 {
		t.Errorf("dbo.Orders = %+v", orders)
	}
	if tables[key("sales", "Invoices")] == nil {
		t.Error("sales.Invoices not loaded")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Error(err)
	}
}
