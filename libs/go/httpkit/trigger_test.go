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

// waitForGuardRelease polls until guard reads false (the detached run
// ended and released it). The two adoption sites poll the same way
// from outside the package (collection's admin test retriggers until
// 202; enrichment's reads h.refreshing.Load() directly) since a
// release has no other external signal.
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

// TestTriggerDetached_ConcurrentTriggerConflicts pins the shape both
// adoption sites need: a second trigger while the first still holds
// the guard gets the 409 problem body (collection's
// rematch_in_progress, enrichment's refresh_in_progress are instances
// of ConflictCode/ConflictDetail) and never reaches Run at all.
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
	// A canary, not a real sweep: if the CAS-fail branch ever reaches
	// Run, this panics loudly instead of passing silently (same style
	// as the two services' own nil-stub canaries).
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

// TestTriggerDetached_PanicInRunIsRecoveredAndLogged pins panic
// containment: a panicking Run must not crash the process (a
// CronJob-driven lever would otherwise crash-loop on a persistently
// bad input), must log PanicMsg with the recovered value, and must
// still release the guard.
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

// TestTriggerDetached_GuardReleasesOnNormalCompletion pins the
// non-panic release path separately from the panic one above: a Run
// that returns normally must still flip the guard back to false so
// the next trigger is admitted.
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

// TestTriggerDetached_ReturnsImmediatelyAndHoldsGuard pins the
// immediate-202 half of the contract: TriggerDetached must not block
// on Run, and the guard must already be held by the time it returns
// (so a second, near-simultaneous trigger reliably conflicts instead
// of racing the CAS).
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

// TestTriggerDetached_RunContextIsDetachedWithBudgetDeadline pins the
// budget context: Run gets a deadline Budget out from now, not the
// trigger request's own context, which the caller may cancel (a
// client disconnect, the server finishing the response) the instant
// TriggerDetached returns.
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
	// The trigger request ends while Run is still executing (checked
	// before releasing Run below, which lets Run's own deferred cancel
	// fire): Run's own context must be unaffected.
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

// TestTriggerDetached_StartedRunsBeforeRunOnTheDetachedGoroutine pins
// the original two call sites' log order: "op started" must be fully
// done before the detached run has done anything at all, not merely
// before TriggerDetached returns to its caller (those are different
// guarantees - the run goroutine is eligible to execute in parallel
// the instant it's spawned). Run blocks on release until told
// otherwise, so the first read of order below is race-free: Run
// cannot have touched order yet regardless of scheduling, which is
// exactly what proves Started already ran to completion first.
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
