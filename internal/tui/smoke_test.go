package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"

	"github.com/simon/crabctl/internal/session"
)

// newSmokeModel returns a minimally-wired Model suitable for calling View().
// Skips DB and executors so the test stays hermetic.
func newSmokeModel() Model {
	ti := textinput.New()
	ti.Focus()
	return Model{
		input:            ti,
		selected:         make(map[string]bool),
		remoteLoading:    make(map[string]bool),
		remoteFailures:   make(map[string]int),
		remoteFailed:     make(map[string]bool),
		autoForward:      make(map[string]bool),
		autoForwardCount: make(map[string]int),
		waitingSince:     make(map[string]time.Time),
		parents:          make(map[string]string),
		foldState:        make(map[string]int),
		lastInteracted:   make(map[string]time.Time),
		width:            120,
		height:           40,
		lastInteraction:  time.Now(),
	}
}

func TestViewEmptyStateAdvertisesOrchestrator(t *testing.T) {
	m := newSmokeModel()
	out := m.View()
	if !strings.Contains(out, "Start an orchestrator") {
		t.Errorf("empty state should advertise the orchestrator CTA, got:\n%s", out)
	}
	if !strings.Contains(out, "ctrl+n") {
		t.Errorf("empty state should mention ctrl+n, got:\n%s", out)
	}
	if !strings.Contains(out, "/orchestrator") {
		t.Errorf("empty state should mention /orchestrator, got:\n%s", out)
	}
}

func TestViewNormalStateShowsRotatingHint(t *testing.T) {
	m := newSmokeModel()
	m.sessions = []session.Session{{Name: "demo", FullName: "crab-demo"}}
	m.filtered = m.sessions
	out := m.View()
	if !strings.Contains(out, fixedHint) {
		t.Errorf("normal view should contain the fixed hint, got:\n%s", out)
	}
	// At hintIndex=0, the first rotating hint should be present.
	if !strings.Contains(out, rotatingHints[0]) {
		t.Errorf("normal view should show rotating hint 0, got:\n%s", out)
	}
}

func TestViewRotatesHintOnTick(t *testing.T) {
	m := newSmokeModel()
	m.sessions = []session.Session{{Name: "demo", FullName: "crab-demo"}}
	m.filtered = m.sessions
	// Force a stale rotation timestamp; rotateHintIfDue should advance.
	m.lastHintRotation = time.Now().Add(-2 * hintRotationInterval)
	m.rotateHintIfDue()
	if m.hintIndex != 1 {
		t.Errorf("hintIndex: got %d, want 1 after stale rotation", m.hintIndex)
	}
	out := m.View()
	if !strings.Contains(out, rotatingHints[1]) {
		t.Errorf("after rotation, view should show rotating hint 1, got:\n%s", out)
	}
}

func TestViewDirPickerOpenSuppressesRotatingHint(t *testing.T) {
	m := newSmokeModel()
	m.sessions = []session.Session{{Name: "demo", FullName: "crab-demo"}}
	m.filtered = m.sessions
	m.dirPicker = openDirPicker(t.TempDir())
	out := m.View()
	if !strings.Contains(out, "Where to spawn the crab?") {
		t.Errorf("picker header missing, got:\n%s", out)
	}
	if strings.Contains(out, fixedHint) {
		t.Errorf("dir-picker mode should suppress fixed hint, got:\n%s", out)
	}
}

func TestOpenSpawnFlowEmptyRoutesToOrchestrator(t *testing.T) {
	m := newSmokeModel()
	// No sessions → Ctrl+N path returns a cmd (the orchestrator spawn) and
	// leaves dirPicker unset.
	ret, cmd := m.openSpawnFlow()
	rm := ret.(Model)
	if rm.dirPicker != nil {
		t.Errorf("empty state should NOT open the dir-picker")
	}
	if cmd == nil {
		t.Errorf("empty state should return a spawn-orchestrator cmd")
	}
}

func TestOpenSpawnFlowWithSessionsOpensPicker(t *testing.T) {
	m := newSmokeModel()
	m.sessions = []session.Session{{Name: "demo", FullName: "crab-demo", WorkDir: t.TempDir()}}
	m.filtered = m.sessions
	ret, _ := m.openSpawnFlow()
	rm := ret.(Model)
	if rm.dirPicker == nil {
		t.Errorf("non-empty state should open the dir-picker")
	}
}

// Ctrl+N on a greyed-out (virtual) parent relaunches that session in place
// instead of opening the picker to nest a child under a session that is gone.
func TestOpenSpawnFlowVirtualRelaunchesInPlace(t *testing.T) {
	m := newSmokeModel()
	m.sessions = []session.Session{
		{Name: "orch", FullName: "crab-orch", Virtual: true},
		{Name: "w1", FullName: "crab-w1", WorkDir: t.TempDir()},
	}
	m.filtered = m.sessions
	m.cursor = 0 // select the virtual parent

	ret, cmd := m.openSpawnFlow()
	rm := ret.(Model)
	if rm.dirPicker != nil {
		t.Errorf("virtual selection should NOT open the dir-picker")
	}
	if cmd == nil {
		t.Fatalf("virtual selection should return a relaunch cmd")
	}
}

// A live selection must still open the picker (child-spawn behavior preserved).
func TestOpenSpawnFlowLiveSelectionStillOpensPicker(t *testing.T) {
	m := newSmokeModel()
	m.sessions = []session.Session{
		{Name: "orch", FullName: "crab-orch", Virtual: true},
		{Name: "w1", FullName: "crab-w1", WorkDir: t.TempDir()},
	}
	m.filtered = m.sessions
	m.cursor = 1 // select the live session

	ret, _ := m.openSpawnFlow()
	if ret.(Model).dirPicker == nil {
		t.Errorf("live selection should still open the dir-picker")
	}
}
