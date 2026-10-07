package billing

import "errors"

type Invoice struct {
	Lines  []int64
	Total  int64
	Issued bool
}

var ErrIssued = errors.New("invoice already issued")

// Edit replaces an invoice's lines.
func (inv *Invoice) Edit(lines []int64) error {
	// rivet:intent BIL-001 — issued invoices are immutable
	if inv.Issued {
		return ErrIssued
	}
	inv.Lines = lines
	inv.recalc()
	return nil
}

// rivet:intent BIL-002
func (inv *Invoice) recalc() {
	var sum int64
	for _, l := range inv.Lines {
		sum += l
	}
	inv.Total = sum
}
