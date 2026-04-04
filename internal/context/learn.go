package context

import (
	"fmt"
	"os"
	"strings"
	"time"
)

const learningsHeader = "## Learnings"

// DocStats holds size metrics for a context document.
type DocStats struct {
	Lines    int
	Learnings int
}

// GetDocStats counts total lines and learning entries in a context document.
func GetDocStats(path string) DocStats {
	data, err := os.ReadFile(path)
	if err != nil {
		return DocStats{}
	}
	content := string(data)
	lines := strings.Count(content, "\n")
	learnings := 0
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "- ") && len(trimmed) > 14 && trimmed[2:6] >= "2020" && trimmed[2:6] <= "2099" {
			learnings++
		}
	}
	return DocStats{Lines: lines, Learnings: learnings}
}

// AppendLearning adds a timestamped learning to a context document.
// Creates a "## Learnings" section if one doesn't exist.
func AppendLearning(path, learning string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	content := string(data)
	date := time.Now().Format("2006-01-02")
	entry := fmt.Sprintf("- %s — %s", date, learning)

	// Find existing Learnings section.
	idx := strings.Index(content, learningsHeader)
	if idx >= 0 {
		// Insert after the header line.
		afterHeader := idx + len(learningsHeader)
		// Skip past the newline after the header.
		if afterHeader < len(content) && content[afterHeader] == '\n' {
			afterHeader++
		}
		// Find the end of the learnings section (next ## heading or end of file).
		rest := content[afterHeader:]
		endIdx := strings.Index(rest, "\n## ")
		if endIdx == -1 {
			// Append at end of file.
			content = strings.TrimRight(content, "\n") + "\n" + entry + "\n"
		} else {
			// Insert before the next section.
			insertAt := afterHeader + endIdx
			content = content[:insertAt] + entry + "\n" + content[insertAt:]
		}
	} else {
		// Add new Learnings section at the end.
		content = strings.TrimRight(content, "\n") + "\n\n" + learningsHeader + "\n\n" + entry + "\n"
	}

	return os.WriteFile(path, []byte(content), 0644)
}
