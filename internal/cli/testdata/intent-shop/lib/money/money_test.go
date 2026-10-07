package money

import "testing"

// rivet:intent MON-001
func TestCentsAreIntegers(t *testing.T) {
	var c Cents = 10
	if c+c != 20 {
		t.Fatal("integer math")
	}
}
