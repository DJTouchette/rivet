package billing

import "testing"

// rivet:intent BIL-001
func TestIssuedInvoiceCannotBeEdited(t *testing.T) {
	inv := &Invoice{Issued: true}
	if err := inv.Edit([]int64{1}); err != ErrIssued {
		t.Fatalf("got %v", err)
	}
}
