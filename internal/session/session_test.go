package session

import (
	"strings"
	"testing"
	"time"
)

func lines(s string) []string {
	return strings.Split(s, "\n")
}

func TestDetectStatus(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect Status
	}{
		{
			name: "idle prompt with shortcuts hint",
			input: `⏺ some output here

❯
───────────────────
  ? for shortcuts`,
			expect: Waiting,
		},
		{
			name: "idle prompt with NBSP",
			input: `⏺ done

❯` + "\u00a0" + `
───────────────────
  ? for shortcuts`,
			expect: Waiting,
		},
		{
			name: "idle prompt with bypass mode",
			input: `⏺ Bash(git status)

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "actively running with esc to interrupt",
			input: `⏺ Read(foo.go)

✻ Thinking...

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt`,
			expect: Running,
		},
		{
			name: "esc to interrupt takes priority over prompt",
			input: `❯
───────────────────
  ? for shortcuts · esc to interrupt`,
			expect: Running,
		},
		{
			name: "permission prompt near bottom",
			input: `⏺ Bash(rm -rf /tmp/test)

  Allow   Deny`,
			expect: Permission,
		},
		{
			name: "allow once permission prompt",
			input: `⏺ Write(foo.txt)

  Allow once   Allow always   Deny`,
			expect: Permission,
		},
		{
			name: "allow/deny in output content does NOT trigger permission",
			input: `⏺ The firewall rules allow traffic and deny bad actors.
  You should allow ingress and deny egress for this policy.

❯
───────────────────
  ? for shortcuts`,
			expect: Waiting,
		},
		{
			name: "active spinner at bottom means running",
			input: `⏺ Read(main.go)

✻ Pondering…`,
			expect: Running,
		},
		{
			name: "active spinner variant ✶",
			input: `⏺ Updated plan

✶ Simmering… (6m 24s)`,
			expect: Running,
		},
		{
			name: "spinner with token count and thinking",
			input: `⏺ Read(main.go)

✶ Transfiguring… (56s · ↓ 1.1k tokens · thinking)`,
			expect: Running,
		},
		{
			name: "spinner variant ✻ with same status line",
			input: `⏺ Read(main.go)

✻ Transfiguring… (53s · ↓ 1.1k tokens · thinking)`,
			expect: Running,
		},
		{
			name: "completed spinner does NOT trigger running",
			input: `✻ Brewed for 39s

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "completed spinner near bottom does NOT trigger running",
			input: `⏺ Done.

✻ Crunched for 5m 1s

❯
───────────────────
  ? for shortcuts`,
			expect: Waiting,
		},
		{
			name: "plan approval menu with ❯ selector",
			input: `╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌
 Claude has written up a plan.

 ❯ 1. Yes, clear context and bypass permissions
   2. Yes, and bypass permissions
   3. Yes, manually approve edits
   4. Type here to tell Claude what to change

 ctrl-g to edit in Nvim · ~/.claude/plans/foo.md`,
			expect: Confirm,
		},
		{
			name: "plan approval without dashed border in capture",
			input: ` Claude has written up a plan. Would you like to proceed?

 ❯ 1. Yes, clear context (52% used) and bypass permissions
   2. Yes, and bypass permissions
   3. Yes, manually approve edits
   4. Type here to tell Claude what to change

 ctrl-g to edit in Nvim · ~/.claude/plans/foo.md`,
			expect: Confirm,
		},
		{
			name: "plan approval long content above dashed border",
			input: ` - item 1
 - item 2
 - item 3
 - item 4
 - item 5
 - item 6
 - item 7
 - item 8
 - item 9
 - item 10
 - item 11
 - item 12
╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌╌

 Claude has written up a plan. Would you like to proceed?

 ❯ 1. Yes, clear context and bypass permissions
   2. Yes, and bypass permissions
   3. Yes, manually approve edits
   4. Type here to tell Claude what to change

 ctrl-g to edit in Nvim · ~/.claude/plans/bar.md`,
			expect: Confirm,
		},
		{
			name: "plan approval minimal just menu items",
			input: `  ❯ 1. Yes, and bypass permissions
    2. Yes, manually approve edits`,
			expect: Confirm,
		},
		{
			name: "empty output",
			input: "",
			expect: Unknown,
		},
		{
			name: "only decoration lines",
			input: `───────────────────
  ? for shortcuts`,
			expect: Unknown,
		},
		{
			name: "braille spinner",
			input: `⏺ Loading...

⠹ Working`,
			expect: Running,
		},
		{
			name: "task done marker with prompt",
			input: `⏺ Done.

TASK DONE!

❯
───────────────────
  ? for shortcuts`,
			expect: TaskDone,
		},
		{
			name: "task done marker in agent output with prompt",
			input: `I've completed all the requested changes. TASK DONE!

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: TaskDone,
		},
		{
			name: "task done not triggered when still running",
			input: `TASK DONE! said earlier

✻ Pondering…`,
			expect: Running,
		},
		{
			name: "prompt with typed text",
			input: `⏺ Done.

❯ implement the feature
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "TASK_DONE underscore variant is not TaskDone",
			input: `TASK_DONE! sent as autoforward message

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "Waiting text in tool output is not running",
			input: `Waiting…

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "ellipsis in action marker is not running",
			input: `⏺ Write(/tmp/very-long-path…)

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "truncation indicator is not running",
			input: `… +30 lines (ctrl+o to expand)

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Waiting,
		},
		{
			name: "spinner above prompt means running not waiting",
			input: ` ⏺ Let me create all the app files.

 ✢ Nesting… (53s · ↓ 738 tokens · thought for 2s)

 ❯`,
			expect: Running,
		},
		{
			name: "spinner above prompt with status bar means running",
			input: ` ⏺ Let me create all the app files.

 ✻ Whisking… (1m 35s · ↓ 2.8k tokens · thought for 2s)

 ❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`,
			expect: Running,
		},
		{
			name: "basic spinner above prompt means running",
			input: ` ⏺ Read(main.go)

 ✽ Thinking…

 ❯`,
			expect: Running,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectStatus(lines(tt.input))
			if got != tt.expect {
				t.Errorf("detectStatus() = %v, want %v", got, tt.expect)
			}
		})
	}
}

func TestDetectMode(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "bypass mode",
			input:  "  ⏵⏵ bypass permissions on (shift+tab to cycle)",
			expect: "bypass",
		},
		{
			name:   "plan mode",
			input:  "  ⏸ plan mode on (shift+tab to cycle)",
			expect: "plan",
		},
		{
			name:   "auto-edit mode",
			input:  "  auto-accept edits on",
			expect: "auto-edit",
		},
		{
			name:   "no mode",
			input:  "  ? for shortcuts",
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseStatusBar(lines(tt.input))
			if got.Mode != tt.expect {
				t.Errorf("parseStatusBar().Mode = %q, want %q", got.Mode, tt.expect)
			}
		})
	}
}

func TestDetectLastAction(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "simple action",
			input:  "⏺ Read(main.go)\n\n❯",
			expect: "Read(main.go)",
		},
		{
			name:   "long action truncated",
			input:  "⏺ This is a very long action description that exceeds the maximum length limit\n\n❯",
			expect: "This is a very long action descriptio...",
		},
		{
			name:   "picks most recent action",
			input:  "⏺ First action\n\n⏺ Second action\n\n❯",
			expect: "Second action",
		},
		{
			name:   "no action",
			input:  "❯",
			expect: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := detectLastAction(lines(tt.input))
			if got != tt.expect {
				t.Errorf("detectLastAction() = %q, want %q", got, tt.expect)
			}
		})
	}
}

func TestIsDecorationLine(t *testing.T) {
	tests := []struct {
		input  string
		expect bool
	}{
		{"───────────────", true},
		{"╌╌╌╌╌╌╌╌╌╌╌╌╌", true},
		{"? for shortcuts", true},
		{"⏵⏵ bypass permissions on (shift+tab to cycle)", true},
		{"2. Yes, and bypass permissions", false},
		{"⏸ plan mode on (shift+tab to cycle)", true},
		{"auto-accept edits on", true},
		{"ctrl-g to edit in Nvim · ~/.claude/plans/foo.md", true},
		{"╭ some box", true},
		{"╰ box end", true},
		{"│ box content", true},
		{"❯ prompt", false},
		{"⏺ Read(foo.go)", false},
		{"some normal output", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := isDecorationLine(tt.input)
			if got != tt.expect {
				t.Errorf("isDecorationLine(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestSortSessions(t *testing.T) {
	sessions := []Session{
		{Name: "idle-old", Status: Waiting, Duration: 7200e9},
		{Name: "running", Status: Running, Duration: 3600e9},
		{Name: "perm", Status: Permission, Duration: 1800e9},
		{Name: "idle-new", Status: Waiting, Duration: 600e9},
		{Name: "unknown", Status: Unknown, Duration: 100e9},
	}
	SortSessions(sessions)

	expected := []string{"perm", "running", "idle-new", "idle-old", "unknown"}
	for i, s := range sessions {
		if s.Name != expected[i] {
			t.Errorf("position %d: got %q, want %q", i, s.Name, expected[i])
		}
	}
}

func TestSortSessionsLocalBeforeRemote(t *testing.T) {
	sessions := []Session{
		{Name: "remote-running", Host: "bay1", Status: Running, Duration: 100e9},
		{Name: "local-waiting", Host: "", Status: Waiting, Duration: 600e9},
		{Name: "remote-perm", Host: "bay1", Status: Permission, Duration: 50e9},
		{Name: "local-running", Host: "", Status: Running, Duration: 200e9},
	}
	SortSessions(sessions)

	expected := []string{"local-running", "local-waiting", "remote-perm", "remote-running"}
	for i, s := range sessions {
		if s.Name != expected[i] {
			t.Errorf("position %d: got %q, want %q", i, s.Name, expected[i])
		}
	}
}

func TestEncodeProjectDir(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"/Users/simon/git/crabctl", "-Users-simon-git-crabctl"},
		{"/home/user/project", "-home-user-project"},
		{"/tmp", "-tmp"},
		{"", ""},
	}
	for _, tt := range tests {
		got := encodeProjectDir(tt.input)
		if got != tt.want {
			t.Errorf("encodeProjectDir(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSeparatorPreventsContentBudgetBurn(t *testing.T) {
	// Lines below the first ─── separator should not count toward the
	// 10-content-line scan budget. With 12 content lines above the
	// separator, detection should still reach the prompt because the
	// filler line below the separator doesn't consume budget.
	output := `line 1
line 2
line 3
line 4
line 5
line 6
line 7
line 8
line 9
line 10
line 11
line 12
───────────────────────────────────────────────────────────────
❯
───────────────────────────────────────────────────────────────
  some future unknown decoration line
  ⏵⏵ bypass permissions on (shift+tab to cycle)`

	got := detectStatus(lines(output))
	if got != Waiting {
		t.Errorf("detectStatus() = %v, want Waiting (separator should protect scan budget)", got)
	}
}

func TestPlanConfirmNoSeparatorsInCapture(t *testing.T) {
	// When the plan text is long, the ╌ dashed line may be outside
	// the 25-line capture window. The numbered menu items alone should
	// still result in Confirm status.
	output := `  ❯ 1. Yes, clear context (52% used) and bypass permissions
    2. Yes, and bypass permissions
    3. Yes, manually approve edits
    4. Type here to tell Claude what to change`

	got := detectStatus(lines(output))
	if got != Confirm {
		t.Errorf("detectStatus() = %v, want Confirm (menu items without ╌ border)", got)
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		seconds int
		expect  string
	}{
		{30, "30s"},
		{90, "1m"},
		{3600, "1h"},
		{3660, "1h 1m"},
		{86400, "1d"},
		{90000, "1d 1h"},
	}

	for _, tt := range tests {
		t.Run(tt.expect, func(t *testing.T) {
			d := time.Duration(tt.seconds) * time.Second
			got := FormatDuration(d)
			if got != tt.expect {
				t.Errorf("FormatDuration(%ds) = %q, want %q", tt.seconds, got, tt.expect)
			}
		})
	}
}
