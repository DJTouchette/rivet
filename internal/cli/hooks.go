package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// ensureHooks adds rivet hooks to .claude/settings.json.
// Non-destructive: preserves existing settings and only adds rivet hooks.
func ensureHooks() (string, error) {
	path := filepath.Join(".claude", "settings.json")

	if err := os.MkdirAll(".claude", 0755); err != nil {
		return "", fmt.Errorf("creating .claude/: %w", err)
	}

	// Write hook scripts.
	scriptDir := filepath.Join(".rivet", "hooks")
	if err := os.MkdirAll(scriptDir, 0755); err != nil {
		return "", fmt.Errorf("creating hooks dir: %w", err)
	}

	scripts := []struct {
		name    string
		content string
	}{
		{"learn-nudge.sh", learnNudgeScript},
		{"compact-check.sh", compactCheckScript},
	}
	for _, s := range scripts {
		p := filepath.Join(scriptDir, s.name)
		if err := os.WriteFile(p, []byte(s.content), 0755); err != nil {
			return "", fmt.Errorf("writing %s: %w", p, err)
		}
	}

	// Load or create settings.json.
	var settings map[string]interface{}
	if data, err := os.ReadFile(path); err == nil {
		if err := json.Unmarshal(data, &settings); err != nil {
			return "", fmt.Errorf("parsing %s: %w", path, err)
		}
	}
	if settings == nil {
		settings = make(map[string]interface{})
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		hooks = make(map[string]interface{})
	}

	// Check if already configured.
	if hasRivetHook(hooks, "PostToolUse", ".rivet/hooks/learn-nudge.sh") &&
		hasRivetHook(hooks, "PostToolUse", ".rivet/hooks/compact-check.sh") {
		return "rivet hooks already configured", nil
	}

	// Remove old Stop hook if it exists.
	removeRivetHook(hooks, "Stop", ".rivet/hooks/learn-nudge.sh")

	// Add PostToolUse hook for learn nudge — fires after MCP recon tool calls.
	// Matcher uses pipe-separated tool names to match recon tools.
	addHook(hooks, "PostToolUse", "mcp__rivet__recon_grep|mcp__rivet__recon_search|mcp__rivet__recon_related|mcp__rivet__recon_context|mcp__rivet__recon_symbols", ".rivet/hooks/learn-nudge.sh")

	// Add PostToolUse hook for compact check — fires after rivet.learn calls.
	addHook(hooks, "PostToolUse", "mcp__rivet__rivet_learn", ".rivet/hooks/compact-check.sh")

	settings["hooks"] = hooks

	data, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshaling settings: %w", err)
	}
	data = append(data, '\n')

	if err := os.WriteFile(path, data, 0644); err != nil {
		return "", fmt.Errorf("writing %s: %w", path, err)
	}

	return "added rivet hooks (learn-nudge + compact-check)", nil
}

func hasRivetHook(hooks map[string]interface{}, event, command string) bool {
	entries, _ := hooks[event].([]interface{})
	for _, entry := range entries {
		em, _ := entry.(map[string]interface{})
		innerHooks, _ := em["hooks"].([]interface{})
		for _, ih := range innerHooks {
			ihm, _ := ih.(map[string]interface{})
			if cmd, _ := ihm["command"].(string); cmd == command {
				return true
			}
		}
	}
	return false
}

func removeRivetHook(hooks map[string]interface{}, event, command string) {
	entries, _ := hooks[event].([]interface{})
	var kept []interface{}
	for _, entry := range entries {
		em, _ := entry.(map[string]interface{})
		innerHooks, _ := em["hooks"].([]interface{})
		var keptInner []interface{}
		for _, ih := range innerHooks {
			ihm, _ := ih.(map[string]interface{})
			if cmd, _ := ihm["command"].(string); cmd != command {
				keptInner = append(keptInner, ih)
			}
		}
		if len(keptInner) > 0 {
			em["hooks"] = keptInner
			kept = append(kept, em)
		}
	}
	if len(kept) > 0 {
		hooks[event] = kept
	} else {
		delete(hooks, event)
	}
}

func addHook(hooks map[string]interface{}, event, matcher, command string) {
	entries, _ := hooks[event].([]interface{})
	entries = append(entries, map[string]interface{}{
		"matcher": matcher,
		"hooks": []interface{}{
			map[string]interface{}{
				"type":    "command",
				"command": command,
			},
		},
	})
	hooks[event] = entries
}

// learnNudgeScript fires after recon MCP tool calls.
// Checks the transcript for prior recon usage without a rivet.learn call.
// Outputs additionalContext that Claude sees and can act on.
const learnNudgeScript = `#!/bin/bash
# Rivet learn nudge — fires after recon MCP tool calls.
# If Claude has used multiple recon tools but hasn't called rivet.learn,
# nudge it to record findings.

input=$(cat)

# Extract transcript_path.
transcript_path=""
if command -v jq >/dev/null 2>&1; then
  transcript_path=$(echo "$input" | jq -r '.transcript_path // empty' 2>/dev/null)
elif command -v python3 >/dev/null 2>&1; then
  transcript_path=$(echo "$input" | python3 -c "import sys,json; print(json.load(sys.stdin).get('transcript_path',''))" 2>/dev/null)
fi

if [ -z "$transcript_path" ] || [ ! -f "$transcript_path" ]; then
  exit 0
fi

# Count recon tool calls in this session.
recon_count=$(grep -cE "recon\.(grep|search|related|context|symbols)" "$transcript_path" 2>/dev/null || echo 0)

# Only nudge after 2+ recon calls (indicates real investigation, not a quick lookup).
if [ "$recon_count" -lt 2 ]; then
  exit 0
fi

# Check if rivet.learn was already called.
if grep -q "rivet\.learn" "$transcript_path" 2>/dev/null; then
  exit 0
fi

# Output nudge as additionalContext so Claude sees it.
echo "If you've discovered anything non-obvious during this investigation (hidden dependencies, performance traps, implicit ordering, gotchas), call rivet.learn with a title and observation. The entry lands in .rivet/learnings/ and is later promoted into a context doc."
`

// compactCheckScript fires after any rivet MCP call.
// Checks if the learning log has grown past the promotion threshold.
const compactCheckScript = `#!/bin/bash
# Rivet compact check — fires after rivet MCP tool calls.
# If un-promoted learnings exceed threshold, nudge to promote.

LEARNING_THRESHOLD=10

if [ ! -d ".rivet/learnings" ]; then
  exit 0
fi

# Count *.md files directly under .rivet/learnings/ (exclude archive/) that
# are not already marked as promoted.
total=0
promoted=0
for f in .rivet/learnings/*.md; do
  [ -f "$f" ] || continue
  total=$((total+1))
  if grep -q "^promoted: true" "$f" 2>/dev/null; then
    promoted=$((promoted+1))
  fi
done
active=$((total-promoted))

if [ "$active" -ge "$LEARNING_THRESHOLD" ]; then
  echo "Learning log has ${active} active entries (threshold: ${LEARNING_THRESHOLD}). Run /rivet-promote-learnings to review, merge, and promote high-value entries into context docs."
fi
`
