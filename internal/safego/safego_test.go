package safego

import (
	"sync"
	"testing"
	"time"

	"github.com/nexspence-oss/nexspence/internal/logger"
)

// A panicking goroutine that Go doesn't guard would crash this test binary
// (Go terminates the whole process on an unrecovered goroutine panic, not
// just the panicking goroutine) — reaching the end of the test at all is
// itself proof the panic was swallowed.
func TestGo_RecoversPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	Go(logger.New("error", "json"), "test-panic", func() {
		defer wg.Done()
		panic("boom")
	})
	if waitTimeout(&wg, 2*time.Second) {
		t.Fatal("goroutine did not complete — panic likely escaped Go's recover")
	}
}

func TestGo_NilLoggerDoesNotPanic(t *testing.T) {
	var wg sync.WaitGroup
	wg.Add(1)
	Go(nil, "test-nil-logger", func() {
		defer wg.Done()
		panic("boom")
	})
	if waitTimeout(&wg, 2*time.Second) {
		t.Fatal("goroutine did not complete with a nil logger")
	}
}

func TestGo_RunsFnToCompletionWhenNoPanic(t *testing.T) {
	ran := false
	var wg sync.WaitGroup
	wg.Add(1)
	Go(logger.New("error", "json"), "test-normal", func() {
		defer wg.Done()
		ran = true
	})
	if waitTimeout(&wg, 2*time.Second) {
		t.Fatal("goroutine did not complete")
	}
	if !ran {
		t.Error("fn did not run")
	}
}

// TestRecover_LoopSurvivesOnePanickingIteration mirrors the persistent-loop
// usage: each iteration wraps its own body with a deferred Recover, so one
// bad iteration doesn't stop the ones after it.
func TestRecover_LoopSurvivesOnePanickingIteration(t *testing.T) {
	log := logger.New("error", "json")
	completed := 0
	for i := 0; i < 3; i++ {
		func() {
			defer Recover(log, "test-loop")()
			completed++
			if i == 1 {
				panic("bad iteration")
			}
		}()
	}
	if completed != 3 {
		t.Errorf("completed = %d, want 3 (the panic in iteration 1 should not have stopped the loop)", completed)
	}
}

func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) (timedOut bool) {
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return false
	case <-time.After(timeout):
		return true
	}
}
