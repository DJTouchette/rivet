package semantic

import "strings"

// Chunking parameters. Small embedding models (bge-small, gte-small) cap input
// around 512 tokens, so a long wiki page must be split into passages. We chunk
// by words with a little overlap so a concept that straddles a boundary still
// lands whole in at least one chunk. Word counts approximate tokens closely
// enough for retrieval purposes (~1 word ≈ 1.3 tokens).
const (
	chunkWords   = 320 // ~420 tokens — comfortably under a 512-token limit
	chunkOverlap = 40  // words repeated between adjacent chunks
)

// chunk splits text into overlapping word windows. Short text (<= chunkWords)
// returns a single chunk. Empty/whitespace-only text returns nil.
//
// Splitting on whitespace deliberately discards markdown structure: for
// retrieval we only need bags of words dense enough for the model to embed, not
// faithful rendering.
func chunk(text string) []string {
	words := strings.Fields(text)
	if len(words) == 0 {
		return nil
	}
	if len(words) <= chunkWords {
		return []string{strings.Join(words, " ")}
	}

	var chunks []string
	step := chunkWords - chunkOverlap // guaranteed > 0 by the consts above
	for start := 0; start < len(words); start += step {
		end := start + chunkWords
		if end > len(words) {
			end = len(words)
		}
		chunks = append(chunks, strings.Join(words[start:end], " "))
		if end == len(words) {
			break
		}
	}
	return chunks
}
