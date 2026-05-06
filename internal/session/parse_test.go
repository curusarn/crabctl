package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func loadFixture(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "captures", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return string(b)
}

func TestParsePaneFixtures(t *testing.T) {
	tests := []struct {
		fixture     string
		wantState   string
		wantBuffer  string // "" means must be empty
		bufferCheck func(t *testing.T, got string)
	}{
		{
			fixture:    "idle.txt",
			wantState:  "idle",
			wantBuffer: "",
		},
		{
			fixture:   "queued.txt",
			wantState: "queued",
			bufferCheck: func(t *testing.T, got string) {
				if got != "Hello, this is a queued message" {
					t.Errorf("input_buffer = %q, want %q", got, "Hello, this is a queued message")
				}
			},
		},
		{
			fixture:    "running.txt",
			wantState:  "running",
			wantBuffer: "",
		},
		{
			fixture:   "permission.txt",
			wantState: "permission",
		},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			raw := loadFixture(t, tt.fixture)
			got := ParsePane(raw)
			if got.State != tt.wantState {
				t.Errorf("state = %q, want %q (status_bar=%q, last_tool_line=%q)", got.State, tt.wantState, got.StatusBar, got.LastToolLine)
			}
			if tt.bufferCheck != nil {
				tt.bufferCheck(t, got.InputBuffer)
			} else if tt.wantBuffer != got.InputBuffer {
				t.Errorf("input_buffer = %q, want %q", got.InputBuffer, tt.wantBuffer)
			}
		})
	}
}

func TestParsePaneInline(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantState  string
		wantBuffer string
	}{
		{
			name: "idle empty prompt",
			input: `⏺ Done.

────────────────────────────────────────
❯
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			wantState:  "idle",
			wantBuffer: "",
		},
		{
			name: "idle with NBSP after caret",
			input: `⏺ Done.

────────────────────────────────────────
❯` + " " + `
────────────────────────────────────────
  ? for shortcuts`,
			wantState:  "idle",
			wantBuffer: "",
		},
		{
			name: "queued single line",
			input: `⏺ Done.

────────────────────────────────────────
❯ implement feature X
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			wantState:  "queued",
			wantBuffer: "implement feature X",
		},
		{
			name: "queued multi-line wrap",
			input: `⏺ Done.

────────────────────────────────────────
❯ first line of buffer
  second line of buffer
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			wantState:  "queued",
			wantBuffer: "first line of buffer\n  second line of buffer",
		},
		{
			name: "running with esc to interrupt overrides empty prompt",
			input: `⏺ Read(main.go)

✽ Thinking…

────────────────────────────────────────
❯
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt`,
			wantState:  "running",
			wantBuffer: "",
		},
		{
			name: "running with text in buffer still classified as running",
			input: `⏺ Read(main.go)

✽ Thinking…

────────────────────────────────────────
❯ already typed follow-up
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt`,
			wantState:  "running",
			wantBuffer: "already typed follow-up",
		},
		{
			name: "permission Allow/Deny pane",
			input: `⏺ Bash(rm -rf /tmp/test)

  Allow   Deny`,
			wantState: "permission",
		},
		{
			name: "permission numbered Yes/No menu",
			input: `⏺ Bash(printf 'hi' > /tmp/x)
  ⎿  Waiting…

────────────────────────────────────────
 Bash command

   printf 'hi' > /tmp/x

 Do you want to proceed?
 ❯ 1. Yes
   2. Yes, and always allow access to tmp/ from this project
   3. No

 Esc to cancel · Tab to amend · ctrl+e to explain`,
			wantState: "permission",
		},
		{
			name: "no rules at all → unknown",
			input: `just some loose text
without any structural markers`,
			wantState:  "unknown",
			wantBuffer: "",
		},
		{
			name: "missing lower rule → fall back to unknown rather than lie",
			input: `⏺ Done.

────────────────────────────────────────
❯ trailing typed text but no closing rule`,
			wantState:  "unknown",
			wantBuffer: "",
		},
		{
			name:       "empty pane",
			input:      "",
			wantState:  "unknown",
			wantBuffer: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParsePane(tt.input)
			if got.State != tt.wantState {
				t.Errorf("state = %q, want %q", got.State, tt.wantState)
			}
			if got.InputBuffer != tt.wantBuffer {
				t.Errorf("input_buffer = %q, want %q", got.InputBuffer, tt.wantBuffer)
			}
		})
	}
}

func TestParsePaneStatusBar(t *testing.T) {
	raw := `⏺ Done.

────────────────────────────────────────
❯
────────────────────────────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle) · ctrl+t to hide tasks`
	got := ParsePane(raw)
	if !strings.Contains(got.StatusBar, "bypass permissions on") {
		t.Errorf("status_bar = %q, want substring %q", got.StatusBar, "bypass permissions on")
	}
}

func TestParsePaneLastToolLine(t *testing.T) {
	raw := `⏺ Read(foo.go)
⏺ Bash(ls -la)

────────────────────────────────────────
❯
────────────────────────────────────────
  ? for shortcuts`
	got := ParsePane(raw)
	if got.LastToolLine != "⏺ Bash(ls -la)" {
		t.Errorf("last_tool_line = %q, want %q", got.LastToolLine, "⏺ Bash(ls -la)")
	}
}
