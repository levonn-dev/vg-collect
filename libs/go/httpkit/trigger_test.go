package httpkit_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/levonn-dev/vgkeep/libs/go/httpkit"
)

func discardLogger() *slog.Logger { return slog.New(slog.NewJSONHandler(io.Discard, nil)) }

// waitForGuardRelease polls until guard reads false (the detached run ended and released it).
func waitForGuardRelease(t *testing.T, guard *atomic.Bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !guard.Load() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("guard never released")
}

func baseOptions(guard *atomic.Bool, logger *slog.Logger, run func(context.Context)) httpkit.TriggerDetachedOptions {
	return httpkit.TriggerDetachedOptions{
		Guard:          guard,
		ConflictCode:   "x_in_progress",
		ConflictDetail: "an x is already running",
		Budget:         time.Minute,
		Logger:         logger,
		PanicMsg:       "x panicked",
		Run:            run,
	}
}

// TestTriggerDetached_ConcurrentTriggerConflicts pins that a second trigger while the first
// holds the guard gets the 409 body and never reaches Run.
func TestTriggerDetached_ConcurrentTriggerConflicts(t *testing.T) {
	var guard atomic.Bool
	started := make(chan struct{})
	release := make(chan struct{})

	w1 := httptest.NewRecorder()
	ok := httpkit.TriggerDetached(w1, httptest.NewRequest(http.MethodPost, "/x", nil),
		baseOptions(&guard, discardLogger(), func(context.Context) {
			close(started)
			<-release
		}))
	if !ok {
		t.Fatal("first trigger must be accepted")
	}
	<-started

	w2 := httptest.NewRecorder()
	// A canary, not a real sweep: if the CAS-fail branch ever reaches Run, this panics loudly.
	ok = httpkit.TriggerDetached(w2, httptest.NewRequest(http.MethodPost, "/x", nil),
		baseOptions(&guard, discardLogger(), func(context.Context) {
			panic("Run must not be called while the guard is held")
		}))
	if ok {
		t.Fatal("second trigger must be refused while the first holds the guard")
	}
	if w2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409", w2.Code)
	}
	var p httpkit.Problem
	if err := json.NewDecoder(w2.Body).Decode(&p); err != nil {
		t.Fatal(err)
	}
	if p.Code != "x_in_progress" || p.Detail != "an x is already running" {
		t.Fatalf("problem = %+v", p)
	}
	if !guard.Load() {
		t.Fatal("a refused trigger must not touch the guard held by the run in flight")
	}
	close(release)
	waitForGuardRelease(t, &guard)
}

// TestTriggerDetached_PanicInRunIsRecoveredAndLogged pins that a panicking Run does not crash
// the process, logs PanicMsg with the recovered value, and still releases the guard.
func TestTriggerDetached_PanicInRunIsRecoveredAndLogged(t *testing.T) {
	var guard atomic.Bool
	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, nil))

	w := httptest.NewRecorder()
	ok := httpkit.TriggerDetached(w, httptest.NewRequest(http.MethodPost, "/x", nil),
		baseOptions(&guard, logger, func(context.Context) { panic("boom") }))
	if !ok {
		t.Fatal("trigger must be accepted before the panicking run detaches")
	}

	waitForGuardRelease(t, &guard)
	logged := buf.String()
	if !strings.Contains(logged, `"msg":"x panicked"`) || !strings.Contains(logged, `"panic":"boom"`) {
		t.Fatalf("panic log missing: %s", logged)
	}
}

// TestTriggerDetached_GuardReleasesOnNormalCompletion pins that a normally-returning Run still releases the guard.
func TestTriggerDetached_GuardReleasesOnNormalCompletion(t *testing.T) {
	var guard atomic.Bool
	done := make(chan struct{})

	w := httptest.NewRecorder()
	ok := httpkit.TriggerDetached(w, httptest.NewRequest(http.MethodPost, "/x", nil),
		baseOptions(&guard, discardLogger(), func(context.Context) { close(done) }))
	if !ok {
		t.Fatal("trigger must be accepted")
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("run never started")
	}
	waitForGuardRelease(t, &guard)
}

// TestTriggerDetached_ReturnsImmediatelyAndHoldsGuard pins that TriggerDetached does not block
// on Run, and the guard is already held by the time it returns.
func TestTriggerDetached_ReturnsImmediatelyAndHoldsGuard(t *testing.T) {
	var guard atomic.Bool
	release := make(chan struct{})

	w := httptest.NewRecorder()
	start := time.Now()
	ok := httpkit.TriggerDetached(w, httptest.NewRequest(http.MethodPost, "/x", nil),
		baseOptions(&guard, discardLogger(), func(context.Context) { <-release }))
	elapsed := time.Since(start)
	if !ok {
		t.Fatal("trigger must be accepted")
	}
	if !guard.Load() {
		t.Fatal("guard must already be held by the time TriggerDetached returns")
	}
	if elapsed > 200*time.Millisecond {
		t.Fatalf("TriggerDetached must return without waiting for Run, took %v", elapsed)
	}
	close(release)
	waitForGuardRelease(t, &guard)
}

// TestTriggerDetached_RunContextIsDetachedWithBudgetDeadline pins that Run gets a deadline
// Budget out from now, not the trigger request's own context, which the caller may cancel.
func TestTriggerDetached_RunContextIsDetachedWithBudgetDeadline(t *testing.T) {
	var guard atomic.Bool
	reqCtx, cancelReq := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodPost, "/x", nil).WithContext(reqCtx)

	var gotCtx context.Context
	inRun := make(chan struct{})
	release := make(chan struct{})
	w := httptest.NewRecorder()
	opts := baseOptions(&guard, discardLogger(), func(ctx context.Context) {
		gotCtx = ctx
		close(inRun)
		<-release
	})
	opts.Budget = 30 * time.Minute
	ok := httpkit.TriggerDetached(w, req, opts)
	if !ok {
		t.Fatal("trigger must be accepted")
	}
	<-inRun
	// The trigger request ends while Run still executes (before releasing Run, so its
	// deferred cancel can fire): Run's own context must be unaffected.
	cancelReq()

	dl, hasDeadline := gotCtx.Deadline()
	if !hasDeadline {
		t.Fatal("run context must carry the budget deadline")
	}
	if until := time.Until(dl); until <= 0 || until > 30*time.Minute {
		t.Fatalf("deadline %v outside the budget window", until)
	}
	if err := gotCtx.Err(); err != nil {
		t.Fatalf("run context must not be canceled by the request ending: %v", err)
	}
	close(release)
	waitForGuardRelease(t, &guard)
}

// TestTriggerDetached_StartedRunsBeforeRunOnTheDetachedGoroutine pins that Started fully
// completes before the detached run does anything, not merely before TriggerDetached returns
// (the goroutine is eligible to run in parallel the instant it's spawned). Run blocks on
// release, so the first read of order is race-free proof Started already ran.
func TestTriggerDetached_StartedRunsBeforeRunOnTheDetachedGoroutine(t *testing.T) {
	var guard atomic.Bool
	var order []string
	release := make(chan struct{})
	done := make(chan struct{})

	opts := baseOptions(&guard, discardLogger(), func(context.Context) {
		<-release
		order = append(order, "run")
		close(done)
	})
	opts.Started = func() { order = append(order, "started") }

	w := httptest.NewRecorder()
	ok := httpkit.TriggerDetached(w, httptest.NewRequest(http.MethodPost, "/x", nil), opts)
	if !ok {
		t.Fatal("trigger must be accepted")
	}
	if got := append([]string{}, order...); len(got) != 1 || got[0] != "started" {
		t.Fatalf("order before Run is released = %v, want [started]", got)
	}

	close(release)
	<-done
	if got := append([]string{}, order...); len(got) != 2 || got[0] != "started" || got[1] != "run" {
		t.Fatalf("order = %v, want [started run]", got)
	}
	waitForGuardRelease(t, &guard)
}
