package session

import (
	"testing"
	"time"
)

func TestBuildTreeNoParents(t *testing.T) {
	sessions := []Session{
		{Name: "a", FullName: "crab-a", Status: Running, Duration: 1 * time.Minute},
		{Name: "b", FullName: "crab-b", Status: Waiting, Duration: 2 * time.Minute},
	}
	result := BuildTree(sessions, nil, nil)
	if len(result) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(result))
	}
	// Should be sorted by status priority (running before waiting)
	if result[0].Name != "a" {
		t.Errorf("expected 'a' first (running), got %q", result[0].Name)
	}
	if result[0].TreeDepth != 0 || result[0].TreePrefix != "" {
		t.Errorf("expected depth 0, no prefix for orphan")
	}
}

func TestBuildTreeWithParent(t *testing.T) {
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "w1", FullName: "crab-w1", Status: Running, Duration: 5 * time.Minute},
		{Name: "w2", FullName: "crab-w2", Status: Running, Duration: 3 * time.Minute},
	}
	parents := map[string]string{
		"crab-w1": "crab-orch",
		"crab-w2": "crab-orch",
	}
	result := BuildTree(sessions, parents, nil)
	// Filter visible
	visible := filterVisible(result)
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible sessions, got %d", len(visible))
	}
	// Parent first
	if visible[0].FullName != "crab-orch" {
		t.Errorf("expected parent first, got %q", visible[0].FullName)
	}
	// All children visible in default fold → HiddenCount = 0
	if visible[0].HiddenCount != 0 {
		t.Errorf("expected hidden count 0 (all visible), got %d", visible[0].HiddenCount)
	}
	// Children with tree prefix
	if visible[1].TreeDepth != 1 {
		t.Errorf("expected depth 1 for child, got %d", visible[1].TreeDepth)
	}
	if visible[1].TreePrefix != "├── " {
		t.Errorf("expected '├── ' prefix, got %q", visible[1].TreePrefix)
	}
	if visible[2].TreePrefix != "└── " {
		t.Errorf("expected '└── ' prefix for last child, got %q", visible[2].TreePrefix)
	}
}

func TestBuildTreeVirtualParent(t *testing.T) {
	sessions := []Session{
		{Name: "w1", FullName: "crab-w1", Status: Running, Duration: 5 * time.Minute},
		{Name: "standalone", FullName: "crab-standalone", Status: Waiting, Duration: 2 * time.Minute},
	}
	parents := map[string]string{
		"crab-w1": "simon", // parent "simon" doesn't exist as a session
	}
	result := BuildTree(sessions, parents, nil)
	visible := filterVisible(result)
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible (1 virtual + 1 child + 1 orphan), got %d", len(visible))
	}
	// Standalone (orphan) should come first (real sessions before virtual)
	if visible[0].FullName != "crab-standalone" {
		t.Errorf("expected standalone first, got %q", visible[0].FullName)
	}
	// Virtual parent
	if !visible[1].Virtual {
		t.Errorf("expected virtual parent")
	}
	if visible[1].FullName != "simon" {
		t.Errorf("expected virtual parent name 'simon', got %q", visible[1].FullName)
	}
	if visible[1].HiddenCount != 0 {
		t.Errorf("expected hidden count 0, got %d", visible[1].HiddenCount)
	}
	// Child under virtual parent
	if visible[2].FullName != "crab-w1" {
		t.Errorf("expected child 'crab-w1', got %q", visible[2].FullName)
	}
	if visible[2].TreePrefix != "└── " {
		t.Errorf("expected '└── ' prefix for only child, got %q", visible[2].TreePrefix)
	}
}

func TestBuildTreeGrandchildren(t *testing.T) {
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "mid", FullName: "crab-mid", Status: Running, Duration: 5 * time.Minute},
		{Name: "leaf", FullName: "crab-leaf", Status: Running, Duration: 2 * time.Minute},
	}
	parents := map[string]string{
		"crab-mid":  "crab-orch",
		"crab-leaf": "crab-mid",
	}
	result := BuildTree(sessions, parents, nil)
	visible := filterVisible(result)
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible sessions, got %d", len(visible))
	}
	// orch -> mid -> leaf
	if visible[0].FullName != "crab-orch" || visible[0].TreeDepth != 0 {
		t.Errorf("expected orch at depth 0")
	}
	if visible[1].FullName != "crab-mid" || visible[1].TreeDepth != 1 {
		t.Errorf("expected mid at depth 1")
	}
	if visible[2].FullName != "crab-leaf" || visible[2].TreeDepth != 2 {
		t.Errorf("expected leaf at depth 2, got %d", visible[2].TreeDepth)
	}
	// Grandchild prefix: mid is last child of orch, so no continuing line
	if visible[2].TreePrefix != "    └── " {
		t.Errorf("expected '    └── ' prefix for grandchild, got %q", visible[2].TreePrefix)
	}
}

func TestBuildTreeRemoteParent(t *testing.T) {
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Host: "", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "w1", FullName: "user-w1", Host: "bay1", Status: Running, Duration: 5 * time.Minute},
	}
	parents := map[string]string{
		"bay1:user-w1": "crab-orch",
	}
	result := BuildTree(sessions, parents, nil)
	visible := filterVisible(result)
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible sessions, got %d", len(visible))
	}
	if visible[0].FullName != "crab-orch" {
		t.Errorf("expected parent first, got %q", visible[0].FullName)
	}
	if visible[1].FullName != "user-w1" || visible[1].TreeDepth != 1 {
		t.Errorf("expected remote child at depth 1")
	}
}

func TestBuildTreeFoldClosed(t *testing.T) {
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "w1", FullName: "crab-w1", Status: Running, Duration: 5 * time.Minute},
		{Name: "w2", FullName: "crab-w2", Status: Running, Duration: 3 * time.Minute},
	}
	parents := map[string]string{
		"crab-w1": "crab-orch",
		"crab-w2": "crab-orch",
	}
	foldState := map[string]int{
		"crab-orch": FoldClosed,
	}
	result := BuildTree(sessions, parents, foldState)
	visible := filterVisible(result)
	if len(visible) != 1 {
		t.Fatalf("expected 1 visible session (parent only), got %d", len(visible))
	}
	if visible[0].FullName != "crab-orch" {
		t.Errorf("expected orch, got %q", visible[0].FullName)
	}
	if visible[0].HiddenCount != 2 {
		t.Errorf("expected hidden count 2, got %d", visible[0].HiddenCount)
	}
	// Hidden children should be preserved in the full result
	hidden := filterHidden(result)
	if len(hidden) != 2 {
		t.Fatalf("expected 2 hidden sessions, got %d", len(hidden))
	}
}

func TestBuildTreeFoldFull(t *testing.T) {
	// 4-level deep tree: orch → mid → sub → leaf
	// Default fold shows 2 levels, so sub and leaf hidden.
	// FoldFull on orch shows all.
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "mid", FullName: "crab-mid", Status: Running, Duration: 5 * time.Minute},
		{Name: "sub", FullName: "crab-sub", Status: Running, Duration: 3 * time.Minute},
		{Name: "leaf", FullName: "crab-leaf", Status: Running, Duration: 2 * time.Minute},
	}
	parents := map[string]string{
		"crab-mid":  "crab-orch",
		"crab-sub":  "crab-mid",
		"crab-leaf": "crab-sub",
	}

	// Default: orch shows 2 levels (mid, sub visible; leaf hidden)
	result := BuildTree(sessions, parents, nil)
	visible := filterVisible(result)
	if len(visible) != 3 {
		t.Fatalf("default: expected 3 visible, got %d", len(visible))
	}
	// sub should show 1 hidden descendant (leaf)
	if visible[2].FullName != "crab-sub" {
		t.Errorf("default: expected sub at index 2, got %q", visible[2].FullName)
	}
	if visible[2].HiddenCount != 1 {
		t.Errorf("default: expected sub hidden count 1, got %d", visible[2].HiddenCount)
	}

	// FoldFull: all 4 visible
	foldState := map[string]int{
		"crab-orch": FoldFull,
	}
	result = BuildTree(sessions, parents, foldState)
	visible = filterVisible(result)
	if len(visible) != 4 {
		t.Fatalf("fold full: expected 4 visible, got %d", len(visible))
	}
	if visible[3].FullName != "crab-leaf" {
		t.Errorf("fold full: expected leaf at index 3, got %q", visible[3].FullName)
	}
	if visible[3].TreeDepth != 3 {
		t.Errorf("fold full: expected leaf depth 3, got %d", visible[3].TreeDepth)
	}
}

func TestBuildTreeDeepTree(t *testing.T) {
	// 6-level deep tree: root → l1 → l2 → l3 → l4 → l5
	// MaxTreeDepth = 5, so FoldFull can show up to depth 5
	sessions := []Session{
		{Name: "root", FullName: "crab-root", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "l1", FullName: "crab-l1", Status: Running, Duration: 9 * time.Minute},
		{Name: "l2", FullName: "crab-l2", Status: Running, Duration: 8 * time.Minute},
		{Name: "l3", FullName: "crab-l3", Status: Running, Duration: 7 * time.Minute},
		{Name: "l4", FullName: "crab-l4", Status: Running, Duration: 6 * time.Minute},
		{Name: "l5", FullName: "crab-l5", Status: Running, Duration: 5 * time.Minute},
	}
	parents := map[string]string{
		"crab-l1": "crab-root",
		"crab-l2": "crab-l1",
		"crab-l3": "crab-l2",
		"crab-l4": "crab-l3",
		"crab-l5": "crab-l4",
	}

	// FoldFull: shows up to MaxTreeDepth (5), so l5 at depth 5 is visible
	foldState := map[string]int{
		"crab-root": FoldFull,
	}
	result := BuildTree(sessions, parents, foldState)
	visible := filterVisible(result)
	if len(visible) != 6 {
		t.Fatalf("expected 6 visible (all levels), got %d", len(visible))
	}
	for i, expected := range []int{0, 1, 2, 3, 4, 5} {
		if visible[i].TreeDepth != expected {
			t.Errorf("session %d: expected depth %d, got %d", i, expected, visible[i].TreeDepth)
		}
	}
	// l5 at depth 5 should have correct prefix (all ancestors are last children)
	if visible[5].TreePrefix != "    "+repeat("    ", 3)+"└── " {
		t.Errorf("unexpected prefix for l5: %q", visible[5].TreePrefix)
	}
}

func TestBuildTreeNestedFold(t *testing.T) {
	// orch → mid → sub, orch → w2
	// mid is FoldClosed, so sub is hidden
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "mid", FullName: "crab-mid", Status: Running, Duration: 5 * time.Minute},
		{Name: "sub", FullName: "crab-sub", Status: Running, Duration: 3 * time.Minute},
		{Name: "w2", FullName: "crab-w2", Status: Running, Duration: 2 * time.Minute},
	}
	parents := map[string]string{
		"crab-mid": "crab-orch",
		"crab-sub": "crab-mid",
		"crab-w2":  "crab-orch",
	}
	foldState := map[string]int{
		"crab-mid": FoldClosed,
	}
	result := BuildTree(sessions, parents, foldState)
	visible := filterVisible(result)
	if len(visible) != 3 {
		t.Fatalf("expected 3 visible (orch, mid, w2), got %d", len(visible))
	}
	// mid should show 1 hidden
	var mid *Session
	for i := range visible {
		if visible[i].FullName == "crab-mid" {
			mid = &visible[i]
			break
		}
	}
	if mid == nil {
		t.Fatal("mid not found in visible")
	}
	if mid.HiddenCount != 1 {
		t.Errorf("expected mid hidden count 1, got %d", mid.HiddenCount)
	}
	// sub should be hidden
	hidden := filterHidden(result)
	if len(hidden) != 1 || hidden[0].FullName != "crab-sub" {
		t.Errorf("expected sub to be hidden, got %v", hidden)
	}
}

func TestBuildTreeHiddenPreserved(t *testing.T) {
	// Verify that hidden sessions are preserved in the result for rebuild.
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "w1", FullName: "crab-w1", Status: Running, Duration: 5 * time.Minute},
	}
	parents := map[string]string{
		"crab-w1": "crab-orch",
	}
	foldState := map[string]int{
		"crab-orch": FoldClosed,
	}
	result := BuildTree(sessions, parents, foldState)
	if len(result) != 2 {
		t.Fatalf("expected 2 total sessions, got %d", len(result))
	}

	// Now unfold — rebuild from previous result
	delete(foldState, "crab-orch")
	result2 := BuildTree(result, parents, foldState)
	visible := filterVisible(result2)
	if len(visible) != 2 {
		t.Fatalf("expected 2 visible after unfold, got %d", len(visible))
	}
	if visible[0].FullName != "crab-orch" || visible[1].FullName != "crab-w1" {
		t.Errorf("expected orch then w1, got %q then %q", visible[0].FullName, visible[1].FullName)
	}
}

func TestBuildTreeGrandchildPrefix(t *testing.T) {
	// Verify prefix when parent has siblings (continuing line)
	// mid sorts before w2 (shorter duration), so mid is NOT last child
	sessions := []Session{
		{Name: "orch", FullName: "crab-orch", Status: Waiting, Duration: 10 * time.Minute},
		{Name: "mid", FullName: "crab-mid", Status: Running, Duration: 3 * time.Minute},
		{Name: "leaf", FullName: "crab-leaf", Status: Running, Duration: 2 * time.Minute},
		{Name: "w2", FullName: "crab-w2", Status: Running, Duration: 5 * time.Minute},
	}
	parents := map[string]string{
		"crab-mid":  "crab-orch",
		"crab-w2":   "crab-orch",
		"crab-leaf": "crab-mid",
	}
	result := BuildTree(sessions, parents, nil)
	visible := filterVisible(result)
	if len(visible) != 4 {
		t.Fatalf("expected 4 visible, got %d", len(visible))
	}
	// mid is not last child (w2 follows), so leaf should have "│   └── "
	var leaf *Session
	for i := range visible {
		if visible[i].FullName == "crab-leaf" {
			leaf = &visible[i]
			break
		}
	}
	if leaf == nil {
		t.Fatal("leaf not found")
	}
	if leaf.TreePrefix != "│   └── " {
		t.Errorf("expected '│   └── ' for grandchild with non-last parent, got %q", leaf.TreePrefix)
	}
}

func TestSessionKey(t *testing.T) {
	if SessionKey("", "crab-foo") != "crab-foo" {
		t.Error("local key should be just fullName")
	}
	if SessionKey("bay1", "user-foo") != "bay1:user-foo" {
		t.Error("remote key should be host:fullName")
	}
}

func TestParseSessionKey(t *testing.T) {
	fn, h := parseSessionKey("crab-foo")
	if fn != "crab-foo" || h != "" {
		t.Errorf("expected ('crab-foo', ''), got (%q, %q)", fn, h)
	}
	fn, h = parseSessionKey("bay1:user-foo")
	if fn != "user-foo" || h != "bay1" {
		t.Errorf("expected ('user-foo', 'bay1'), got (%q, %q)", fn, h)
	}
}

// --- helpers ---

func filterVisible(sessions []Session) []Session {
	var out []Session
	for _, s := range sessions {
		if !s.TreeHidden {
			out = append(out, s)
		}
	}
	return out
}

func filterHidden(sessions []Session) []Session {
	var out []Session
	for _, s := range sessions {
		if s.TreeHidden {
			out = append(out, s)
		}
	}
	return out
}

func repeat(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}
