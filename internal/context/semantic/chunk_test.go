package semantic

import (
	"strings"
	"testing"
)

func TestChunk(t *testing.T) {
	if chunk("") != nil {
		t.Error("empty text should chunk to nil")
	}
	if chunk("   \n\t ") != nil {
		t.Error("whitespace-only should chunk to nil")
	}

	// Short text → one chunk.
	short := "hello world this is short"
	if got := chunk(short); len(got) != 1 || got[0] != short {
		t.Errorf("short chunk = %v", got)
	}

	// Long text → multiple overlapping chunks covering everything.
	words := make([]string, 0, 1000)
	for i := 0; i < 1000; i++ {
		words = append(words, "w"+itoa(i))
	}
	long := strings.Join(words, " ")
	chunks := chunk(long)
	if len(chunks) < 2 {
		t.Fatalf("expected multiple chunks, got %d", len(chunks))
	}
	// First chunk starts at w0, and the last word of the corpus appears in the
	// final chunk (full coverage).
	if !strings.HasPrefix(chunks[0], "w0 ") {
		t.Errorf("first chunk should start at w0: %.20q", chunks[0])
	}
	if !strings.Contains(chunks[len(chunks)-1], "w999") {
		t.Error("last chunk should cover the final word")
	}
	// Adjacent chunks overlap (chunkOverlap words shared).
	firstWords := strings.Fields(chunks[0])
	secondWords := strings.Fields(chunks[1])
	overlap := 0
	tail := map[string]bool{}
	for _, w := range firstWords[len(firstWords)-chunkOverlap:] {
		tail[w] = true
	}
	for _, w := range secondWords[:chunkOverlap] {
		if tail[w] {
			overlap++
		}
	}
	if overlap == 0 {
		t.Error("adjacent chunks should overlap")
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [12]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}
