package cmd

import (
	"strings"
	"sync/atomic"
	"testing"
)

// fakeExecutor records SendKeys calls and serves a scripted sequence of
// pane outputs for CapturePaneOutput. The scripted outputs drive the
// retry loop in sendMessage: while the captured pane parses as Waiting,
// the loop should keep resending Enter until either status transitions
// or the retry cap is reached.
type fakeExecutor struct {
	sendKeysCalls   []struct{ session, text string }
	outputs         []string
	captureIdx      int
	captureErr      error
	sendKeysErr     error
}

func (f *fakeExecutor) CapturePaneOutput(_ string, _ int) (string, error) {
	if f.captureErr != nil {
		return "", f.captureErr
	}
	if f.captureIdx >= len(f.outputs) {
		// Once the scripted outputs are exhausted, keep returning the last
		// one so the loop terminates after hitting its retry cap.
		return f.outputs[len(f.outputs)-1], nil
	}
	out := f.outputs[f.captureIdx]
	f.captureIdx++
	return out, nil
}

func (f *fakeExecutor) SendKeys(session, text string) error {
	f.sendKeysCalls = append(f.sendKeysCalls, struct{ session, text string }{session, text})
	return f.sendKeysErr
}

const waitingPane = `⏺ ready

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`

const runningPane = `⏺ ready

✻ Thinking… (1s · esc to interrupt)

❯
───────────────────
  ⏵⏵ bypass permissions on (shift+tab to cycle)`

func TestSendMessage_FirstCaptureRunning_NoRetries(t *testing.T) {
	enterCount := int32(0)
	prev := sendEnter
	sendEnter = func(string) { atomic.AddInt32(&enterCount, 1) }
	defer func() { sendEnter = prev }()

	fe := &fakeExecutor{outputs: []string{runningPane}}
	if err := sendMessage(fe, "crab-test", "hello"); err != nil {
		t.Fatalf("sendMessage error: %v", err)
	}
	if len(fe.sendKeysCalls) != 1 {
		t.Fatalf("expected 1 SendKeys call, got %d", len(fe.sendKeysCalls))
	}
	if got := atomic.LoadInt32(&enterCount); got != 0 {
		t.Fatalf("expected 0 Enter retries when Claude is already Running, got %d", got)
	}
}

func TestSendMessage_StuckWaiting_RetriesCappedAtTwo(t *testing.T) {
	enterCount := int32(0)
	prev := sendEnter
	sendEnter = func(string) { atomic.AddInt32(&enterCount, 1) }
	defer func() { sendEnter = prev }()

	// Pane stays in Waiting for the whole retry window — simulates a
	// 3KB paste whose Enter was absorbed AND whose follow-up retries
	// were also absorbed.
	fe := &fakeExecutor{outputs: []string{waitingPane}}
	if err := sendMessage(fe, "crab-test", "x"); err != nil {
		t.Fatalf("sendMessage error: %v", err)
	}
	if got := atomic.LoadInt32(&enterCount); got != 2 {
		t.Fatalf("expected exactly 2 Enter retries (cap), got %d", got)
	}
}

func TestSendMessage_RetryThenRunning(t *testing.T) {
	enterCount := int32(0)
	prev := sendEnter
	sendEnter = func(string) { atomic.AddInt32(&enterCount, 1) }
	defer func() { sendEnter = prev }()

	// First check: still Waiting (Enter was absorbed into paste).
	// After one retry, Claude starts processing.
	fe := &fakeExecutor{outputs: []string{waitingPane, runningPane}}
	if err := sendMessage(fe, "crab-test", "x"); err != nil {
		t.Fatalf("sendMessage error: %v", err)
	}
	if got := atomic.LoadInt32(&enterCount); got != 1 {
		t.Fatalf("expected 1 Enter retry then exit, got %d", got)
	}
}

func TestSendMessage_LargeMessagePassthrough(t *testing.T) {
	// Verify sendMessage doesn't mangle large messages — the paste is
	// handed to SendKeys verbatim. The actual settle is in
	// tmux.SendKeys; this just guards the cmd-layer contract.
	prev := sendEnter
	sendEnter = func(string) {}
	defer func() { sendEnter = prev }()

	big := strings.Repeat("abcdefghij", 300) // 3000 chars
	fe := &fakeExecutor{outputs: []string{runningPane}}
	if err := sendMessage(fe, "crab-test", big); err != nil {
		t.Fatalf("sendMessage error: %v", err)
	}
	if len(fe.sendKeysCalls) != 1 {
		t.Fatalf("expected 1 SendKeys call, got %d", len(fe.sendKeysCalls))
	}
	if fe.sendKeysCalls[0].text != big {
		t.Fatalf("SendKeys text mangled: len got=%d want=%d", len(fe.sendKeysCalls[0].text), len(big))
	}
}
