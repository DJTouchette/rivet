package witness

// PinnedVersion is the github.com/djtouchette/witness module version go.mod
// pins, duplicated here so the rest of rivet has a name to point at.
//
// Siblings are consumed as tagged module versions, never `replace` directives:
// a change in ../witness does nothing here until it is tagged and go.mod is
// bumped. That is easy to forget when both repositories are open at once, and
// the thing it breaks is not the build — it is the capability descriptions in
// internal/capabilities/builtins.go, which are the only account of witness's
// behaviour an agent ever sees.
//
// Describing behaviour the embedded build does not have is worse than
// describing nothing. A description that says "an empty tests[] is safe when
// summary.unmapped is empty" evaluates to "safe" on every run against a witness
// that has no summary.unmapped — the fail-closed advice becomes a fail-open
// rule, which is the exact false green it was written to prevent. So the
// descriptions are written to be true of ANY witness rivet might embed, and
// TestPinnedVersionMatchesGoMod plus TestDescriptionsOnlyNameFlagsWitnessAccepts
// hold them to it.
const PinnedVersion = "v0.5.0"
