package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"coldflow/internal/domain"
	"coldflow/internal/engine"
)

func TestDraftArrowNavigationAndResize(t *testing.T) {
	m := New(nil, true)
	m.tab = 3
	m.Snap = engine.Snapshot{Demo: true, Drafts: []domain.Draft{{ID: "one", Revision: 1, Subject: "First", Body: "First draft"}, {ID: "two", Revision: 1, Subject: "Second", Body: "Second draft"}}}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 26})
	if !strings.Contains(m.View().Content, "First draft") {
		t.Fatal("missing draft")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyRight})
	if m.selected[3] != 1 || !strings.Contains(m.View().Content, "Second draft") {
		t.Fatal("right did not select next draft")
	}
	m.Update(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.selected[3] != 0 {
		t.Fatal("left did not select previous draft")
	}
	m.Update(tea.WindowSizeMsg{Width: 40, Height: 15})
	if !strings.Contains(m.View().Content, "COLDFLOW") {
		t.Fatal("narrow view lost title")
	}
}
func TestRepeatedEnterWhileSavingIsIgnored(t *testing.T) {
	m := New(nil, true)
	m.tab = 3
	m.busy = true
	m.Snap.Drafts = []domain.Draft{{ID: "one", Revision: 1}}
	_, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd != nil {
		t.Fatal("repeated Enter queued another action")
	}
}
