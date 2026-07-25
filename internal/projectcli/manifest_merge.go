package projectcli

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Manifest key names, mirroring capabilities.Manifest / capabilities.ManifestCap.
//
// They are spelled out here because the merge edits the YAML document rather
// than a struct. .rivet/capabilities.yaml is a file rivet explicitly tells users
// to edit — it is where typed params get added — and a struct round-trip
// rewrites it wholesale: every comment gone, `params:` re-rendered with the
// zero value of every field it doesn't set. Editing nodes touches only the keys
// discovery is authoritative for.
const (
	manifestKeyCLI         = "cli"
	manifestKeyCaps        = "capabilities"
	manifestKeyName        = "name"
	manifestKeyDescription = "description"
	manifestKeyCommand     = "command"
	manifestKeyOutput      = "output"
	manifestKeySafety      = "safety"
)

// MergeStatus is what happened to one capability during a merge.
type MergeStatus string

const (
	// StatusAdded — the capability wasn't in the manifest.
	StatusAdded MergeStatus = "added"
	// StatusUpdated — discovered metadata replaced what was in the manifest.
	StatusUpdated MergeStatus = "updated"
	// StatusUnchanged — the manifest already matched discovery.
	StatusUnchanged MergeStatus = "unchanged"
	// StatusKept — discovery disagreed, and the manifest's value was protected.
	StatusKept MergeStatus = "kept"
)

// CapMerge records what happened to a single capability, so the command can say
// it out loud. A merge that quietly rewrites a file users hand-edit is the same
// bug as one that quietly refuses to.
type CapMerge struct {
	Name    string
	Status  MergeStatus
	Changed []string // fields written, e.g. "description", "safety: safe -> dangerous"
	Kept    []string // discovered values rejected, with the reason
}

// MergeResult is the outcome of merging a discovery run into the manifest.
type MergeResult struct {
	Path       string
	Discovered int
	Caps       []CapMerge
}

// Count returns how many capabilities ended in the given status.
func (r *MergeResult) Count(s MergeStatus) int {
	n := 0
	for _, c := range r.Caps {
		if c.Status == s {
			n++
		}
	}
	return n
}

// Summary renders a header line plus one line per capability. Every capability
// appears — including the ones nothing happened to, because "left alone" is the
// answer a user re-running register-cli most needs and least expects to guess.
func (r *MergeResult) Summary() []string {
	lines := []string{fmt.Sprintf("%s — %d discovered: %d added, %d updated, %d unchanged, %d left alone",
		r.Path, r.Discovered,
		r.Count(StatusAdded), r.Count(StatusUpdated), r.Count(StatusUnchanged), r.Count(StatusKept))}

	glyph := map[MergeStatus]string{
		StatusAdded:     "+",
		StatusUpdated:   "~",
		StatusUnchanged: "=",
		StatusKept:      "!",
	}

	for _, c := range r.Caps {
		line := fmt.Sprintf("  %s %-9s %s", glyph[c.Status], c.Status, c.Name)
		if len(c.Changed) > 0 {
			line += " (" + strings.Join(c.Changed, ", ") + ")"
		}
		lines = append(lines, line)
		for _, kept := range c.Kept {
			lines = append(lines, "      kept "+kept+" — pass --force to overwrite")
		}
	}
	return lines
}

// MergeManifest merges discovered capabilities into the manifest at path,
// rewriting it in place, and reports what it did to each one.
//
// Policy, and why:
//
//   - New capabilities are added.
//   - description, command and output are refreshed from discovery. These are
//     mechanical facts only the CLI knows; when a subcommand gets renamed, a
//     manifest that keeps the stale value doesn't preserve a preference, it
//     preserves a broken tool. Merging by name and skipping every existing
//     entry made the discover contract write-once.
//   - safety is refreshed only when discovery is *stricter*. Relaxing a level
//     the manifest already carries needs --force. Discovery defaults an
//     unlabelled command to "dangerous" precisely because that axis fails
//     closed; silently reverting a hand-tightened level would undo that, while
//     silently accepting a loosened one would reopen it.
//   - params are never touched. They cannot appear in discovery output at all,
//     so anything rewriting them can only be destroying work.
//
// An existing file that doesn't parse is an error rather than something to
// overwrite: whatever is in there is a user's, and rendering a fresh document
// over it would delete it.
func MergeManifest(path, cli string, caps []DiscoveredCapability, force bool) (*MergeResult, error) {
	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("reading manifest: %w", err)
	}

	doc, err := manifestDoc(existing)
	if err != nil {
		return nil, err
	}
	root := doc.Content[0]

	setMapValue(root, manifestKeyCLI, scalarNode(cli))

	capsNode := mapValue(root, manifestKeyCaps)
	if capsNode == nil || capsNode.Kind != yaml.SequenceNode {
		capsNode = &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		setMapValue(root, manifestKeyCaps, capsNode)
	}

	result := &MergeResult{Path: path, Discovered: len(caps)}
	for _, dc := range caps {
		if entry := findCapEntry(capsNode, dc.Name); entry != nil {
			result.Caps = append(result.Caps, mergeCapEntry(entry, dc, force))
			continue
		}
		capsNode.Content = append(capsNode.Content, newCapEntry(dc))
		result.Caps = append(result.Caps, CapMerge{Name: dc.Name, Status: StatusAdded})
	}

	data, err := encodeNode(doc)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, fmt.Errorf("creating manifest directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return nil, fmt.Errorf("writing manifest: %w", err)
	}
	return result, nil
}

// manifestDoc returns the document to merge into: the existing one, or an empty
// mapping when there is no file yet.
//
// The whole document is carried through rather than just its root mapping,
// because the file's leading comment block — the one explaining how to write
// params — hangs off the document node, not off any key in it.
func manifestDoc(existing []byte) (*yaml.Node, error) {
	empty := &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
	if len(bytes.TrimSpace(existing)) == 0 {
		return empty, nil
	}

	var doc yaml.Node
	if err := yaml.Unmarshal(existing, &doc); err != nil {
		return nil, fmt.Errorf("parsing manifest: %w (fix it or move it aside; refusing to overwrite it)", err)
	}
	if len(doc.Content) == 0 {
		return empty, nil
	}
	if doc.Content[0].Kind != yaml.MappingNode {
		return nil, fmt.Errorf("manifest is not a YAML mapping; refusing to overwrite it")
	}
	return &doc, nil
}

// mergeCapEntry applies the discovered fields to an existing manifest entry.
func mergeCapEntry(entry *yaml.Node, dc DiscoveredCapability, force bool) CapMerge {
	m := CapMerge{Name: dc.Name, Status: StatusUnchanged}

	for _, f := range []struct {
		key string
		val string
	}{
		{manifestKeyDescription, dc.Description},
		{manifestKeyOutput, dc.Output},
	} {
		if f.val == "" {
			continue
		}
		if old := scalarValue(mapValue(entry, f.key)); old != f.val {
			setMapValue(entry, f.key, scalarNode(f.val))
			m.Changed = append(m.Changed, f.key)
		}
	}

	if len(dc.Command) > 0 && !sameStrings(sequenceValues(mapValue(entry, manifestKeyCommand)), dc.Command) {
		setMapValue(entry, manifestKeyCommand, stringSeqNode(dc.Command))
		m.Changed = append(m.Changed, manifestKeyCommand)
	}

	if dc.Safety != "" {
		old := scalarValue(mapValue(entry, manifestKeySafety))
		switch {
		case old == dc.Safety:
			// nothing to do
		case force || stricter(old, dc.Safety):
			setMapValue(entry, manifestKeySafety, scalarNode(dc.Safety))
			m.Changed = append(m.Changed, fmt.Sprintf("%s: %s -> %s", manifestKeySafety, displaySafety(old), dc.Safety))
		default:
			m.Kept = append(m.Kept, fmt.Sprintf("%s: %s (discovery says %s)", manifestKeySafety, displaySafety(old), dc.Safety))
		}
	}

	switch {
	case len(m.Changed) > 0:
		m.Status = StatusUpdated
	case len(m.Kept) > 0:
		m.Status = StatusKept
	}
	return m
}

func displaySafety(s string) string {
	if s == "" {
		return "(unset)"
	}
	return s
}

// safetyRank orders the levels by how much they restrict execution. Only
// "dangerous" is actually gated by the executor, but the ordering is what the
// manifest, the config and the MCP server all document.
var safetyRank = map[string]int{"safe": 0, "guarded": 1, "dangerous": 2}

// stricter reports whether the discovered level restricts more than the one
// already in the manifest. An unrecognised or missing existing level has nothing
// worth protecting, so discovery wins; an unrecognised discovered level never
// overrides a valid one.
func stricter(existing, discovered string) bool {
	dr, ok := safetyRank[discovered]
	if !ok {
		return false
	}
	er, ok := safetyRank[existing]
	if !ok {
		return true
	}
	return dr > er
}

// newCapEntry builds a manifest entry, in the field order the starter manifests
// use so hand-written and generated entries read the same.
func newCapEntry(dc DiscoveredCapability) *yaml.Node {
	entry := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	setMapValue(entry, manifestKeyName, scalarNode(dc.Name))
	setMapValue(entry, manifestKeyDescription, scalarNode(dc.Description))
	setMapValue(entry, manifestKeyCommand, stringSeqNode(dc.Command))
	setMapValue(entry, manifestKeyOutput, scalarNode(dc.Output))
	setMapValue(entry, manifestKeySafety, scalarNode(dc.Safety))
	return entry
}

// findCapEntry returns the mapping for a capability by name, or nil.
func findCapEntry(caps *yaml.Node, name string) *yaml.Node {
	for _, entry := range caps.Content {
		if entry.Kind != yaml.MappingNode {
			continue
		}
		if scalarValue(mapValue(entry, manifestKeyName)) == name {
			return entry
		}
	}
	return nil
}

func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	if mapping == nil || mapping.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			return mapping.Content[i+1]
		}
	}
	return nil
}

// setMapValue replaces a key's value, or appends the pair when absent. The
// existing key node is reused rather than rebuilt, because it carries whatever
// comment the user wrote above that key.
func setMapValue(mapping *yaml.Node, key string, value *yaml.Node) {
	for i := 0; i+1 < len(mapping.Content); i += 2 {
		if mapping.Content[i].Value == key {
			mapping.Content[i+1] = value
			return
		}
	}
	mapping.Content = append(mapping.Content, scalarNode(key), value)
}

func scalarValue(n *yaml.Node) string {
	if n == nil {
		return ""
	}
	return n.Value
}

func sequenceValues(n *yaml.Node) []string {
	if n == nil || n.Kind != yaml.SequenceNode {
		return nil
	}
	out := make([]string, 0, len(n.Content))
	for _, item := range n.Content {
		out = append(out, item.Value)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func scalarNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

// stringSeqNode renders a command as a flow sequence — `command: [query, status]`
// — matching the starter manifests rather than exploding every argv into its own
// line.
func stringSeqNode(values []string) *yaml.Node {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, v := range values {
		n.Content = append(n.Content, scalarNode(v))
	}
	return n
}

func encodeNode(node *yaml.Node) ([]byte, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(node); err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}
	if err := enc.Close(); err != nil {
		return nil, fmt.Errorf("marshaling manifest: %w", err)
	}
	return buf.Bytes(), nil
}
