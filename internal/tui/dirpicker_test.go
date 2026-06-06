package tui

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func TestFuzzyFilter(t *testing.T) {
	entries := []string{"alpha", "beta", "Crab", "delta", "alpha-2"}

	cases := []struct {
		query string
		want  []string
	}{
		{"", []string{"alpha", "beta", "Crab", "delta", "alpha-2"}},
		{"alp", []string{"alpha", "alpha-2"}},
		{"CR", []string{"Crab"}}, // case-insensitive
		{"a", []string{"alpha", "beta", "Crab", "delta", "alpha-2"}},
		{"zzz", nil},
		{"-2", []string{"alpha-2"}},
	}

	for _, c := range cases {
		got := fuzzyFilter(entries, c.query)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("fuzzyFilter(%q): got %v, want %v", c.query, got, c.want)
		}
	}
}

func TestUniqueSessionName(t *testing.T) {
	cases := []struct {
		name  string
		taken map[string]bool
		base  string
		want  string
	}{
		{"empty taken", map[string]bool{}, "myrepo", "myrepo"},
		{"one taken", map[string]bool{"myrepo": true}, "myrepo", "myrepo-2"},
		{"two taken", map[string]bool{"myrepo": true, "myrepo-2": true}, "myrepo", "myrepo-3"},
		{"sanitized", map[string]bool{}, "my repo!", "my-repo"},
		{"all invalid -> 'crab'", map[string]bool{}, "!!!", "crab"},
		{"all invalid with crab taken", map[string]bool{"crab": true}, "!!!", "crab-2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := uniqueSessionName(c.base, c.taken)
			if got != c.want {
				t.Errorf("uniqueSessionName(%q, %v) = %q, want %q", c.base, c.taken, got, c.want)
			}
		})
	}
}

func TestSanitizeSessionName(t *testing.T) {
	cases := map[string]string{
		"hello":      "hello",
		"my repo":    "my-repo",
		"a/b/c":      "a-b-c",
		"  spaced  ": "spaced",
		"---weird":   "weird",
		"u_score":    "u_score",
		"!!!":        "",
	}
	for in, want := range cases {
		got := sanitizeSessionName(in)
		if got != want {
			t.Errorf("sanitizeSessionName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReadSubdirs(t *testing.T) {
	root := t.TempDir()
	// Make some dirs + files + a dotfile dir.
	for _, sub := range []string{"alpha", "beta", "Charlie", ".hidden"} {
		if err := os.Mkdir(filepath.Join(root, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "afile.txt"), []byte("hi"), 0644); err != nil {
		t.Fatal(err)
	}

	got := readSubdirs(root)
	want := []string{"Charlie", "alpha", "beta"}
	// readSubdirs sorts alphabetically (case-sensitive — Go's sort.Strings).
	sort.Strings(want)
	if !reflect.DeepEqual(got, want) {
		t.Errorf("readSubdirs: got %v, want %v", got, want)
	}
}

// TestPickerStateMachineNavigation exercises the pure navigation logic
// (descend, go up, filter, filter clears on dir change) without spinning
// up Bubble Tea.
func TestPickerStateMachineNavigation(t *testing.T) {
	root := t.TempDir()
	for _, sub := range []string{"alpha", "beta", "gamma"} {
		if err := os.Mkdir(filepath.Join(root, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	// alpha has a child dir 'inner'
	if err := os.Mkdir(filepath.Join(root, "alpha", "inner"), 0755); err != nil {
		t.Fatal(err)
	}

	p := openDirPicker(root)
	if p.Cwd != root {
		t.Fatalf("Cwd: got %q, want %q", p.Cwd, root)
	}
	if !reflect.DeepEqual(p.Entries, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("Entries: got %v", p.Entries)
	}

	// Apply filter "be"
	p.Filter = "be"
	p.applyFilter()
	if !reflect.DeepEqual(p.Entries, []string{"beta"}) {
		t.Fatalf("after filter: got %v", p.Entries)
	}
	if p.Cursor != 0 {
		t.Fatalf("cursor after filter: got %d", p.Cursor)
	}

	// Enter 'beta'
	p.enterSelected()
	if filepath.Base(p.Cwd) != "beta" {
		t.Fatalf("after enter: cwd %q", p.Cwd)
	}
	// Filter should reset on dir change
	if p.Filter != "" {
		t.Fatalf("filter not reset: %q", p.Filter)
	}

	// Go up
	p.goUp()
	if p.Cwd != root {
		t.Fatalf("after goUp: cwd %q, want %q", p.Cwd, root)
	}

	// Navigate to 'alpha' by cursor
	p.Cursor = 0 // alpha
	p.enterSelected()
	if filepath.Base(p.Cwd) != "alpha" {
		t.Fatalf("after enter alpha: cwd %q", p.Cwd)
	}
	if !reflect.DeepEqual(p.Entries, []string{"inner"}) {
		t.Fatalf("alpha's subdirs: got %v", p.Entries)
	}
}

func TestPickerGoUpSelectsLeavingDir(t *testing.T) {
	// Set up: root/{alpha,beta,gamma}, descend into 'beta', go back up.
	// Cursor should land on 'beta' (index 1, since alphabetical).
	root := t.TempDir()
	for _, sub := range []string{"alpha", "beta", "gamma"} {
		if err := os.Mkdir(filepath.Join(root, sub), 0755); err != nil {
			t.Fatal(err)
		}
	}
	p := openDirPicker(root)
	p.Cursor = 1 // beta
	p.enterSelected()
	if filepath.Base(p.Cwd) != "beta" {
		t.Fatalf("descend: cwd %q", p.Cwd)
	}
	p.goUp()
	if p.Cwd != root {
		t.Fatalf("goUp: cwd %q, want %q", p.Cwd, root)
	}
	if p.Cursor != 1 {
		t.Errorf("goUp cursor: got %d (=%q), want 1 (=beta)", p.Cursor, p.Entries[p.Cursor])
	}
}

func TestPickerOpenWithMissingDirFallsBack(t *testing.T) {
	// Non-existent start dir should walk up until something exists.
	root := t.TempDir()
	missing := filepath.Join(root, "does-not-exist", "deeper")
	p := openDirPicker(missing)
	if p.Cwd != root && !filepath.HasPrefix(p.Cwd, "/") {
		t.Fatalf("did not fall back to a real dir: %q", p.Cwd)
	}
}
