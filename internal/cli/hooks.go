package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Rivet used to ship two bash hooks — .rivet/hooks/learn-nudge.sh and
// compact-check.sh — that inferred agent state by grepping Claude Code's
// transcript JSONL. That approach was wrong twice over.
//
// It was wrong in principle: rivet is the MCP server, so it already observes
// every tool call directly. Reconstructing that from an undocumented transcript
// format owned by another tool is guesswork that breaks silently whenever the
// format shifts, and logic living inside a Go string literal can't be tested.
//
// It was also wrong in practice. The scripts counted calls with
// `grep -cE "recon\.(grep|search|...)"` — an escaped literal dot — while the
// transcript records the sanitized MCP name `recon_grep`, so the pattern never
// matched. The count then ran through `grep -c ... || echo 0`, which yields the
// two-line string "0\n0" because grep -c prints 0 *and* exits 1 on no match,
// making the `-lt` comparison abort. The two bugs cancelled into the opposite
// of the intended behavior: the "only nudge after 2+ investigations" throttle
// never engaged, so the nudge fired on the first recon call of every session.
//
// The MCP server does all of this properly and under test — see
// contextFirstMessage, learnNudgeMessage and promoteMessage in internal/mcp.
// It tracks recon calls in session state, suppresses the context-first nudge
// once rivet.context-show is called, resets the counter on rivet.learn, and
// counts real un-promoted files for the promotion nudge.
//
// So the hooks are gone. removeLegacyHooks cleans them out of projects that ran
// an older `rivet init`; leaving them behind would double-nudge on every call.

// legacyHookScripts are the hook scripts older versions of rivet wrote into
// .rivet/hooks/.
var legacyHookScripts = []string{"learn-nudge.sh", "compact-check.sh"}

// removeLegacyHooks deletes the retired bash nudge hooks and unregisters them
// from .claude/settings.json. It is non-destructive: hooks rivet didn't install
// are preserved, and unrelated settings are untouched. Safe to run repeatedly
// and on projects that never had the hooks.
func removeLegacyHooks() (string, error) {
	scriptDir := filepath.Join(".rivet", "hooks")

	var removedScripts int
	for _, name := range legacyHookScripts {
		path := filepath.Join(scriptDir, name)
		err := os.Remove(path)
		switch {
		case err == nil:
			removedScripts++
		case os.IsNotExist(err):
			// Already gone — nothing to do.
		default:
			return "", fmt.Errorf("removing %s: %w", path, err)
		}
	}

	// Drop .rivet/hooks/ once it's empty, so the retired concept leaves no
	// trace. A non-empty dir means the user put something there; leave it.
	if entries, err := os.ReadDir(scriptDir); err == nil && len(entries) == 0 {
		os.Remove(scriptDir)
	}

	unregistered, err := unregisterLegacyHooks(filepath.Join(".claude", "settings.json"))
	if err != nil {
		return "", err
	}

	if removedScripts == 0 && unregistered == 0 {
		return "no legacy hooks to remove", nil
	}
	return fmt.Sprintf("removed %d legacy hook script(s) and %d settings entry(ies) — nudges now come from the MCP server", removedScripts, unregistered), nil
}

// unregisterLegacyHooks removes rivet's retired hook commands from a Claude
// Code settings file, returning how many entries it dropped. A missing file is
// not an error.
func unregisterLegacyHooks(path string) (int, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("reading %s: %w", path, err)
	}

	var settings map[string]interface{}
	if err := json.Unmarshal(data, &settings); err != nil {
		return 0, fmt.Errorf("parsing %s: %w", path, err)
	}

	hooks, _ := settings["hooks"].(map[string]interface{})
	if hooks == nil {
		return 0, nil
	}

	// Older rivet versions registered these under PostToolUse, and one even
	// earlier version used Stop. Sweep every event so no stale copy survives.
	var removed int
	for _, name := range legacyHookScripts {
		command := filepath.Join(".rivet", "hooks", name)
		for event := range hooks {
			removed += removeRivetHook(hooks, event, command)
		}
	}

	if removed == 0 {
		return 0, nil
	}

	if len(hooks) == 0 {
		delete(settings, "hooks")
	} else {
		settings["hooks"] = hooks
	}

	out, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return 0, fmt.Errorf("marshaling settings: %w", err)
	}
	out = append(out, '\n')

	if err := os.WriteFile(path, out, 0644); err != nil {
		return 0, fmt.Errorf("writing %s: %w", path, err)
	}
	return removed, nil
}

// removeRivetHook strips every hook entry matching command from one event,
// returning the number removed. Entries holding other commands keep those, and
// an event left with no entries is deleted outright.
func removeRivetHook(hooks map[string]interface{}, event, command string) int {
	entries, ok := hooks[event].([]interface{})
	if !ok {
		return 0
	}

	var removed int
	kept := make([]interface{}, 0, len(entries))
	for _, entry := range entries {
		em, ok := entry.(map[string]interface{})
		if !ok {
			kept = append(kept, entry)
			continue
		}
		innerHooks, ok := em["hooks"].([]interface{})
		if !ok {
			kept = append(kept, entry)
			continue
		}

		keptInner := make([]interface{}, 0, len(innerHooks))
		for _, ih := range innerHooks {
			ihm, _ := ih.(map[string]interface{})
			if cmd, _ := ihm["command"].(string); cmd == command {
				removed++
				continue
			}
			keptInner = append(keptInner, ih)
		}

		// An entry whose only hook was ours goes away with it; one that still
		// has hooks keeps its matcher and the survivors.
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
	return removed
}
