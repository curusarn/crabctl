package tmux

import "testing"

func TestStripDimText(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect string
	}{
		{
			name:   "no ANSI codes",
			input:  "hello world",
			expect: "hello world",
		},
		{
			name:   "empty string",
			input:  "",
			expect: "",
		},
		{
			name:   "dim text removed",
			input:  "real \x1b[2mghost\x1b[22m text",
			expect: "real \x1b[22m text",
		},
		{
			name:   "bright-black (SGR 90) removed",
			input:  "prompt\x1b[90msuggestion\x1b[39m rest",
			expect: "prompt\x1b[39m rest",
		},
		{
			name:   "reverse video (SGR 7) removed",
			input:  "before\x1b[7mreversed\x1b[27mafter",
			expect: "before\x1b[27mafter",
		},
		{
			name:   "reset (SGR 0) ends dim",
			input:  "\x1b[2mdim\x1b[0mvisible",
			expect: "\x1b[0mvisible",
		},
		{
			name:   "bare reset ESC[m ends dim",
			input:  "\x1b[2mdim\x1b[mvisible",
			expect: "\x1b[mvisible",
		},
		{
			name:   "24-bit color with 2 not treated as dim",
			input:  "\x1b[38;2;128;0;255mcolored text\x1b[0m",
			expect: "\x1b[38;2;128;0;255mcolored text\x1b[0m",
		},
		{
			name:   "256-color with 5 not treated as dim",
			input:  "\x1b[38;5;196mred text\x1b[0m",
			expect: "\x1b[38;5;196mred text\x1b[0m",
		},
		{
			name:   "background 24-bit color not treated as dim",
			input:  "\x1b[48;2;0;0;0mtext\x1b[0m",
			expect: "\x1b[48;2;0;0;0mtext\x1b[0m",
		},
		{
			name:   "combined codes with dim",
			input:  "\x1b[1;2mbold+dim\x1b[22mnormal",
			expect: "\x1b[22mnormal",
		},
		{
			name:   "dim inside 24-bit color context",
			input:  "\x1b[38;2;100;100;100;2mdim after color\x1b[0mok",
			expect: "\x1b[0mok",
		},
		{
			name:   "multiple dim/undim toggles",
			input:  "a\x1b[2mb\x1b[22mc\x1b[2md\x1b[0me",
			expect: "a\x1b[22mc\x1b[0me",
		},
		{
			name:   "non-SGR ANSI codes preserved",
			input:  "\x1b[2Jclear screen\x1b[H",
			expect: "\x1b[2Jclear screen\x1b[H",
		},
		{
			name:   "dim at end of string strips remainder",
			input:  "visible\x1b[2mghost",
			expect: "visible",
		},
		{
			name:   "Claude ghost text pattern: prompt then dim suggestion",
			input:  "❯ \x1b[90mfix the bug in auth\x1b[39m",
			expect: "❯ \x1b[39m",
		},
		{
			name:   "SGR 90 with other codes combined",
			input:  "\x1b[1;90mbright-black bold\x1b[0mnormal",
			expect: "\x1b[0mnormal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripDimText(tt.input)
			if got != tt.expect {
				t.Errorf("stripDimText()\n  got:    %q\n  expect: %q", got, tt.expect)
			}
		})
	}
}

func TestParseSGRCodes(t *testing.T) {
	tests := []struct {
		name   string
		params string
		expect []string
	}{
		{
			name:   "empty",
			params: "",
			expect: []string{""},
		},
		{
			name:   "single code",
			params: "2",
			expect: []string{"2"},
		},
		{
			name:   "multiple codes",
			params: "1;2;4",
			expect: []string{"1", "2", "4"},
		},
		{
			name:   "24-bit fg color skipped",
			params: "38;2;128;0;255",
			expect: []string{},
		},
		{
			name:   "256-color fg skipped",
			params: "38;5;196",
			expect: []string{},
		},
		{
			name:   "24-bit bg color skipped",
			params: "48;2;0;0;0",
			expect: []string{},
		},
		{
			name:   "code after 24-bit color",
			params: "38;2;128;0;255;1",
			expect: []string{"1"},
		},
		{
			name:   "dim after 24-bit color",
			params: "38;2;100;100;100;2",
			expect: []string{"2"},
		},
		{
			name:   "bold and bright-black",
			params: "1;90",
			expect: []string{"1", "90"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseSGRCodes(tt.params)
			if len(got) != len(tt.expect) {
				t.Fatalf("parseSGRCodes(%q) = %v, want %v", tt.params, got, tt.expect)
			}
			for i := range got {
				if got[i] != tt.expect[i] {
					t.Errorf("parseSGRCodes(%q)[%d] = %q, want %q", tt.params, i, got[i], tt.expect[i])
				}
			}
		})
	}
}
