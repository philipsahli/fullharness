package session

import (
	"errors"
	"slices"

	"github.com/philipsahli/fullharness/internal/tracker"
)

// Errors of LinkSpec.
var (
	ErrNotIdea    = errors.New("this Session is not an Idea")
	ErrIdeaLinked = errors.New("this Idea is already linked to a Spec")
)

// Idea is fh's record of an Idea: a Feature at the Idea Stage which, until
// it has a Spec, is the named Session that Grill new idea started
// (GLOSSARY.md). The tracker has nothing to show for it yet, so fh keeps it
// (ADR 0004). A Session that is no Idea has an empty Name.
type Idea struct {
	Name string `json:"idea,omitempty"`
	// KnownSpecs are the Specs the tracker had when the Idea started; only a
	// Spec that appears later can be the Idea's. SpecsRead is false until
	// the tracker was read once, e.g. while gh was logged out at the start.
	KnownSpecs []string `json:"knownSpecs,omitempty"`
	SpecsRead  bool     `json:"specsRead,omitempty"`
}

// IdeaRecord is one Idea as the board shows it: its Session grills it until
// `/to-spec` writes its Spec (spec #1, story 74).
type IdeaRecord struct {
	Session string
	// Spec is the Spec you linked the Idea to; empty until then.
	Spec string
	Idea
}

// Ideas returns project's Ideas: its Grill new idea Sessions, live or
// Exited. Closing the Session drops its Idea.
func (m *Manager) Ideas(project string) []IdeaRecord {
	var out []IdeaRecord
	for _, s := range m.all() {
		s.mu.Lock()
		if s.sum.Project == project && s.idea.Name != "" && !s.closed {
			r := IdeaRecord{Session: s.sum.ID, Idea: s.idea}
			r.KnownSpecs = slices.Clone(s.idea.KnownSpecs)
			if s.sum.Link != nil {
				r.Spec = s.sum.Link.Feature
			}
			out = append(out, r)
		}
		s.mu.Unlock()
	}
	return out
}

// SetKnownSpecs records the Specs the tracker has, for an Idea whose start
// couldn't read them; an Idea that has them keeps its own.
func (m *Manager) SetKnownSpecs(id string, specs []string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	if s.idea.SpecsRead || s.idea.Name == "" {
		s.mu.Unlock()
		return nil
	}
	s.idea.KnownSpecs, s.idea.SpecsRead = slices.Clone(specs), true
	s.mu.Unlock()
	m.persist()
	return nil
}

// LinkSpec links Idea Session id to the Spec its grilling produced: the
// Idea leaves the board's Idea column, and the Session shows its Spec.
func (m *Manager) LinkSpec(id, spec string) error {
	s, err := m.find(id)
	if err != nil {
		return err
	}
	s.mu.Lock()
	switch {
	case s.idea.Name == "":
		s.mu.Unlock()
		return ErrNotIdea
	case s.sum.Link != nil:
		s.mu.Unlock()
		return ErrIdeaLinked
	}
	s.sum.Link = &tracker.Link{Feature: spec}
	s.mu.Unlock()
	m.cfg.Log.Info("idea linked", "session", id, "spec", spec)
	m.persist()
	s.publish()
	return nil
}
