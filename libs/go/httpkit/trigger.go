package httpkit

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// TriggerDetachedOptions configures one detached admin-job trigger.
// Guard is caller-owned - never shared across two different jobs -
// and every string, the panic message, and the sweep itself belong to
// the calling service; TriggerDetached only owns the mechanism around
// them. Guard, ConflictCode, ConflictDetail, Budget, Logger, PanicMsg,
// and Run are required (a nil Guard or Run panics; an empty Budget
// means every run times out immediately). Started is the only
// optional field and is skipped when nil.
type TriggerDetachedOptions struct {
	// Guard is CompareAndSwap'd false->true to admit the trigger, and
	// Store'd back to false when the detached run ends - panic or
	// normal return alike.
	Guard *atomic.Bool
	// ConflictCode and ConflictDetail write the 409 problem body when
	// Guard is already held.
	ConflictCode   string
	ConflictDetail string
	// Started, when set, runs synchronously right after Guard is won
	// and before Run is detached onto its own goroutine - the caller's
	// hook for a pre-detach log line on the trigger request's own
	// context (Run's context is a detached Background(), the wrong
	// one for a line tied to this request). Running it here, ahead of
	// the goroutine spawn, gives the caller's line a real
	// happens-before over anything Run itself logs, matching what a
	// single unbroken function body would have guaranteed for free.
	Started func()
	// Budget bounds the context.Background() Run receives - not the
	// trigger request's own context, which callers are free to cancel
	// the instant TriggerDetached returns.
	Budget time.Duration
	// Logger and PanicMsg record a panic recovered from Run.
	Logger   *slog.Logger
	PanicMsg string
	// Run is the sweep body, called on the detached goroutine with
	// the budget-bound context.
	Run func(ctx context.Context)
}

// TriggerDetached is the shared skeleton behind an admin lever that
// answers immediately and keeps working after the response returns:
// collection's entry rematch and enrichment's catalog refresh are the
// two sites this was extracted from - a third such lever would
// otherwise be a third hand copy of the same six steps.
//
// CompareAndSwap admits one trigger at a time. A trigger that loses
// the race gets the 409 problem body written here and TriggerDetached
// returns false, having never called Started or Run - the caller's
// cue to return without writing a body of its own. A trigger that
// wins runs Started (if set), then detaches Run and returns true
// immediately, before Run has done any work, so the caller can write
// its own 202 body. Guard is already held by the time TriggerDetached
// returns, so a near-simultaneous second trigger reliably conflicts
// instead of racing the CAS.
func TriggerDetached(w http.ResponseWriter, r *http.Request, opts TriggerDetachedOptions) bool {
	if !opts.Guard.CompareAndSwap(false, true) {
		WriteProblemFields(w, r, http.StatusConflict, opts.ConflictCode, opts.ConflictDetail)
		return false
	}
	if opts.Started != nil {
		opts.Started()
	}
	go func() {
		defer opts.Guard.Store(false)
		// Detached from the request context: the trigger has already
		// returned by the time this runs, so Background() plus its own
		// budget is the only context that makes sense here.
		ctx, cancel := context.WithTimeout(context.Background(), opts.Budget)
		defer cancel()
		// Registered last so it unwinds first: a panic inside Run (a
		// malformed payload, a nil field breaking an assumed contract)
		// is contained here instead of killing the process - a
		// CronJob-driven lever would otherwise crash-loop on a
		// persistently bad input. The guard release and context cancel
		// above still run afterward as usual.
		defer func() {
			if v := recover(); v != nil {
				opts.Logger.ErrorContext(ctx, opts.PanicMsg, "panic", v)
			}
		}()
		opts.Run(ctx)
	}()
	return true
}
