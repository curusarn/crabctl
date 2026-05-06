package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/simon/crabctl/internal/session"
)

var (
	captureLines int
	captureJSON  bool
	captureState bool
)

var captureCmd = &cobra.Command{
	Use:   "capture <[host:]name>",
	Short: "Capture pane output with ghost text stripped",
	Long: `Capture tmux pane output from a Claude session, stripping autocomplete
ghost text and ANSI codes. Drop-in replacement for tmux capture-pane
that produces clean output safe for LLM consumption.

Equivalent to: tmux capture-pane -t SESSION -p -S -LINES
but also strips dim/autocomplete suggestions that Claude Code renders.

With --json, emits a structured object with the parsed session state
(idle / queued / running / permission / errored / unknown), the input
buffer, the most recent ⏺ tool line, the status bar, and the raw pane.

With --state, emits just the state word so callers can switch on it
without parsing JSON.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		host, name := parseHostName(args[0])
		exec := resolveExecutor(host)
		fullName := resolveFullName(exec, name)

		if !exec.HasSession(fullName) {
			return fmt.Errorf("session %q not found", args[0])
		}

		output, err := exec.CapturePaneOutput(fullName, captureLines)
		if err != nil {
			return fmt.Errorf("capture failed: %w", err)
		}

		if captureJSON || captureState {
			parsed := session.ParsePane(output)
			if captureState {
				fmt.Fprintln(os.Stdout, parsed.State)
				return nil
			}
			payload := struct {
				Session      string `json:"session"`
				State        string `json:"state"`
				InputBuffer  string `json:"input_buffer"`
				LastToolLine string `json:"last_tool_line"`
				StatusBar    string `json:"status_bar"`
				Raw          string `json:"raw"`
			}{
				Session:      args[0],
				State:        parsed.State,
				InputBuffer:  parsed.InputBuffer,
				LastToolLine: parsed.LastToolLine,
				StatusBar:    parsed.StatusBar,
				Raw:          output,
			}
			enc := json.NewEncoder(os.Stdout)
			enc.SetEscapeHTML(false)
			enc.SetIndent("", "  ")
			return enc.Encode(payload)
		}

		fmt.Fprint(os.Stdout, output)
		return nil
	},
}

func init() {
	captureCmd.Flags().IntVarP(&captureLines, "lines", "S", 30, "Number of lines to capture from the bottom")
	captureCmd.Flags().BoolVar(&captureJSON, "json", false, "Emit parsed pane state as JSON")
	captureCmd.Flags().BoolVar(&captureState, "state", false, "Emit only the parsed state word (idle/queued/running/permission/errored/unknown)")
	rootCmd.AddCommand(captureCmd)
}
