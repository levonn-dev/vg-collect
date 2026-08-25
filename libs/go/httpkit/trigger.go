package httpkit

import (
	"context"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"
)

// TriggerDetachedOptions configures one detached admin-job trigger. Guard is caller-owned,
// never shared across two different jobs; every string, the panic message, and the sweep
// itself belong to the calling service. Guard, ConflictCode, ConflictDetail, Budget, Logger,
// PanicMsg, and Run are required (nil Guard or Run panics; an empty Budget times out
// immediately); Started is optional and skipped when nil.
type TriggerDetachedOptions struct {
	// Guard is CompareAndSwap'd false->true to admit the trigger, and Store'd back to false
	// when the detached run ends, panic or normal return alike.
	Guard *atomic.Bool
	// ConflictCode and ConflictDetail write the 409 problem body when Guard is already held.
	ConflictCode   string
	ConflictDetail string
	// Started, when set, runs synchronously after Guard is won and before Run detaches - the
	// caller's hook for a pre-detach log line on the trigger request's own context (Run gets a
	// detached Background()). Running it here gives the caller's line a happens-before over
	// Run's own logging.
	Started func()
	// Budget bounds the context.Background() Run receives, not the trigger request's own
	// context, which callers are free to cancel once TriggerDetached returns.
	Budget time.Duration
	// Logger and PanicMsg record a panic recovered from Run.
	Logger   *slog.Logger
	PanicMsg string
	// Run is the sweep body, called on the detached goroutine with
	// the budget-bound context.
	Run func(ctx context.Context)
}

// TriggerDetached is the shared skeleton for an admin lever that answers immediately and keeps
// working after the response returns. CompareAndSwap admits one trigger at a time: a losing
// trigger gets the 409 body written here and returns false without calling Started or Run (the
// caller's cue to write no body itself); a winning trigger runs Started, detaches Run, and
// returns true before Run does any work, so the caller can write its own 202. Guard is already
// held by return time, so a near-simultaneous trigger reliably conflicts, not races the CAS.
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
		// Detached from the request context: the trigger has already returned by the time this
		// runs, so Background() plus its own budget is the only context that makes sense.
		ctx, cancel := context.WithTimeout(context.Background(), opts.Budget)
		defer cancel()
		// Registered last so it unwinds first: a panic inside Run is contained here instead of
		// killing the process (a CronJob-driven lever would otherwise crash-loop on bad input).
		// The guard release and context cancel above still run afterward as usual.
		defer func() {
			if v := recover(); v != nil {
				opts.Logger.ErrorContext(ctx, opts.PanicMsg, "panic", v)
			}
		}()
		opts.Run(ctx)
	}()
	return true
}
