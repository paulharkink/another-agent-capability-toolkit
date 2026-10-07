package tui

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/paulharkink/another-agent-capability-toolkit/internal/viewmodel"
)

const teaTestCommandTimeout = 45 * time.Second

// runTeaCmd executes a Bubble Tea command tree as the runtime does: batch
// members run concurrently, and each intermediate message is delivered through
// Update before any command it schedules is run. The final operationMsg is
// returned so the caller can deliver completion at the point asserted by its
// test. Setup operations can stream progress until that final message arrives.
func runTeaCmd(t *testing.T, model *Model, cmd tea.Cmd) tea.Msg {
	t.Helper()
	if cmd == nil {
		t.Fatal("cannot run a nil Tea command")
	}
	type result struct{ msg tea.Msg }
	results := make(chan result, 64)
	pending := 0
	launch := func(command tea.Cmd) {
		if command == nil {
			return
		}
		pending++
		go func() {
			results <- result{msg: command()}
		}()
	}
	launch(cmd)
	timer := time.NewTimer(teaTestCommandTimeout)
	defer timer.Stop()
	for pending > 0 {
		select {
		case <-timer.C:
			if model != nil {
				model.setupOperationPending = false
				model.setupOperationID++
			}
			t.Fatalf("Tea command did not produce an operation message within %s", teaTestCommandTimeout)
			return nil
		case result := <-results:
			pending--
			switch message := result.msg.(type) {
			case nil:
				continue
			case tea.BatchMsg:
				for _, child := range message {
					launch(child)
				}
				continue
			case operationMsg:
				return message
			default:
				if model != nil {
					updated, followUp := model.Update(message)
					if updatedModel, ok := updated.(*Model); ok {
						model = updatedModel
					}
					launch(followUp)
				}
			}
		}
	}
	t.Fatalf("Tea command completed without an operation message")
	return nil
}

func TestRunTeaCmdExecutesBatchAndDispatchesIntermediateMessages(t *testing.T) {
	m := NewContext(t.Context(), &setupBackendFixture{})
	m.setupOperationID = 1
	m.setupOperationPending = true
	events := make(chan viewmodel.OperationProgress, 1)
	m.setupProgressEvents = events
	done := make(chan struct{})
	m.setupProgressDone = done
	defer close(done)
	events <- viewmodel.OperationProgress{Step: "building", Output: "still building"}
	terminal := operationMsg{origin: "Catalog", output: "finished"}
	cmd := tea.Batch(func() tea.Msg { time.Sleep(10 * time.Millisecond); return terminal }, waitSetupProgress(1, m.setupProgressEvents, m.setupProgressDone, m.ctx))

	started := time.Now()
	got := runTeaCmd(t, m, cmd)
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("batch command did not resolve promptly: %s", elapsed)
	}
	if got != terminal {
		t.Fatalf("terminal message = %#v, want %#v", got, terminal)
	}
	if m.progressStep != "building" || m.progressOutput != "still building" {
		t.Fatalf("intermediate progress was not delivered to the model: step=%q output=%q", m.progressStep, m.progressOutput)
	}
}
