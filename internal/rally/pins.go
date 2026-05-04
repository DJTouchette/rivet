// Package rally provides a rivet-side adapter for rally's pinned tickets.
//
// The adapter implements pins.Provider by reading rally's on-disk artifacts
// directly — .rally/pins.json and .rally/tickets/*.md — rather than importing
// rally's internal packages. The on-disk format is the contract; this keeps
// rivet decoupled from rally's Go module.
package rally

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/djtouchette/rivet/internal/pins"
)

const (
	pinsPath    = ".rally/pins.json"
	ticketsDir  = ".rally/tickets"
	source      = "rally"
	uriPrefix   = "rally://pinned/"
	mimeType    = "text/markdown"
)

// rallyPin matches the on-disk shape of an entry in .rally/pins.json.
type rallyPin struct {
	TicketID string    `json:"ticket_id"`
	PinnedAt time.Time `json:"pinned_at"`
	Note     string    `json:"note,omitempty"`
}

type pinsFile struct {
	Pins []rallyPin `json:"pins"`
}

// pinsDirPerm and pinsFilePerm match what rally itself writes, so toggling
// pins from rivet doesn't change file/dir permissions out from under rally.
const (
	pinsDirPerm  = 0755
	pinsFilePerm = 0644
)

// PinProvider reads pinned tickets from a rally-managed directory.
type PinProvider struct {
	// Root is the directory rally writes into (typically "." — the project root).
	Root string
}

// NewPinProvider returns a provider rooted at the current working directory.
func NewPinProvider() *PinProvider {
	return &PinProvider{Root: "."}
}

// Source identifies this provider in pin URIs.
func (p *PinProvider) Source() string { return source }

// List returns all pinned tickets. Missing pins.json or tickets dir are
// treated as "no pins" rather than errors — rally may not be initialized
// in this project.
func (p *PinProvider) List() ([]pins.Item, error) {
	rallyPins, err := p.readPins()
	if err != nil {
		return nil, err
	}
	if len(rallyPins) == 0 {
		return nil, nil
	}

	titles := p.scanTicketTitles()

	items := make([]pins.Item, 0, len(rallyPins))
	for _, rp := range rallyPins {
		title := titles[rp.TicketID]
		name := rp.TicketID
		if title != "" {
			name = fmt.Sprintf("%s — %s", rp.TicketID, title)
		}
		desc := "Pinned rally ticket"
		if rp.Note != "" {
			desc = rp.Note
		}
		items = append(items, pins.Item{
			URI:         uriPrefix + rp.TicketID,
			Name:        name,
			Description: desc,
			MimeType:    mimeType,
		})
	}
	return items, nil
}

// Read returns the markdown body of a single pinned ticket.
func (p *PinProvider) Read(uri string) (pins.Item, error) {
	if !strings.HasPrefix(uri, uriPrefix) {
		return pins.Item{}, fmt.Errorf("rally: unrecognized URI %q", uri)
	}
	id := strings.TrimPrefix(uri, uriPrefix)
	if id == "" {
		return pins.Item{}, fmt.Errorf("rally: empty ticket ID in URI %q", uri)
	}

	rallyPins, err := p.readPins()
	if err != nil {
		return pins.Item{}, err
	}
	var match *rallyPin
	for i := range rallyPins {
		if rallyPins[i].TicketID == id {
			match = &rallyPins[i]
			break
		}
	}
	if match == nil {
		return pins.Item{}, fmt.Errorf("rally: ticket %s is not pinned", id)
	}

	body, title, err := p.readTicketFile(id)
	if err != nil {
		return pins.Item{}, err
	}

	name := id
	if title != "" {
		name = fmt.Sprintf("%s — %s", id, title)
	}
	desc := "Pinned rally ticket"
	if match.Note != "" {
		desc = match.Note
	}
	return pins.Item{
		URI:         uri,
		Name:        name,
		Description: desc,
		MimeType:    mimeType,
		Body:        body,
	}, nil
}

// Pin adds a ticket to .rally/pins.json. Re-pinning is idempotent and does
// not refresh PinnedAt; an empty note never overwrites an existing one.
// This matches rally's own AddPin semantics.
func (p *PinProvider) Pin(id, note string) error {
	if id == "" {
		return fmt.Errorf("rally: empty ticket id")
	}
	pins, err := p.readPins()
	if err != nil {
		return err
	}
	for i := range pins {
		if pins[i].TicketID == id {
			if note != "" {
				pins[i].Note = note
				return p.writePins(pins)
			}
			return nil
		}
	}
	pins = append(pins, rallyPin{
		TicketID: id,
		PinnedAt: time.Now().UTC(),
		Note:     note,
	})
	return p.writePins(pins)
}

// Unpin removes a ticket from .rally/pins.json. Removing an unpinned ticket
// is a no-op.
func (p *PinProvider) Unpin(id string) error {
	pins, err := p.readPins()
	if err != nil {
		return err
	}
	out := pins[:0]
	for _, rp := range pins {
		if rp.TicketID != id {
			out = append(out, rp)
		}
	}
	if len(out) == len(pins) {
		return nil
	}
	return p.writePins(out)
}

func (p *PinProvider) writePins(rp []rallyPin) error {
	dir := filepath.Join(p.Root, ".rally")
	if err := os.MkdirAll(dir, pinsDirPerm); err != nil {
		return fmt.Errorf("rally: creating %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(pinsFile{Pins: rp}, "", "  ")
	if err != nil {
		return fmt.Errorf("rally: encoding pins: %w", err)
	}
	path := filepath.Join(p.Root, pinsPath)
	if err := os.WriteFile(path, data, pinsFilePerm); err != nil {
		return fmt.Errorf("rally: writing %s: %w", path, err)
	}
	return nil
}

func (p *PinProvider) readPins() ([]rallyPin, error) {
	path := filepath.Join(p.Root, pinsPath)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("rally: reading %s: %w", path, err)
	}
	var f pinsFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("rally: decoding %s: %w", path, err)
	}
	return f.Pins, nil
}

// scanTicketTitles walks the rally tickets dir, extracting "# ID: Title"
// from each file's first heading. Failures are silent — title is just a UX
// nicety, not load-bearing.
func (p *PinProvider) scanTicketTitles() map[string]string {
	out := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(p.Root, ticketsDir))
	if err != nil {
		return out
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Root, ticketsDir, e.Name()))
		if err != nil {
			continue
		}
		id, title := parseFirstHeading(string(data))
		if id != "" {
			out[id] = title
		}
	}
	return out
}

// readTicketFile finds the markdown file for a ticket ID and returns its
// full body plus the parsed title.
func (p *PinProvider) readTicketFile(id string) (body, title string, err error) {
	entries, err := os.ReadDir(filepath.Join(p.Root, ticketsDir))
	if err != nil {
		return "", "", fmt.Errorf("rally: reading tickets dir: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".md") {
			continue
		}
		// rally writes files as "{provider}-{id}.md" — match by suffix to
		// stay agnostic to provider.
		if !strings.HasSuffix(e.Name(), "-"+id+".md") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(p.Root, ticketsDir, e.Name()))
		if err != nil {
			return "", "", fmt.Errorf("rally: reading ticket %s: %w", id, err)
		}
		_, t := parseFirstHeading(string(data))
		return string(data), t, nil
	}
	return "", "", fmt.Errorf("rally: no markdown file for ticket %s", id)
}

// parseFirstHeading parses rally's "# {ID}: {Title}" first line.
func parseFirstHeading(content string) (id, title string) {
	for _, line := range strings.SplitN(content, "\n", 2) {
		if !strings.HasPrefix(line, "# ") {
			return "", ""
		}
		rest := strings.TrimPrefix(line, "# ")
		colon := strings.Index(rest, ": ")
		if colon < 0 {
			return "", strings.TrimSpace(rest)
		}
		return strings.TrimSpace(rest[:colon]), strings.TrimSpace(rest[colon+2:])
	}
	return "", ""
}
