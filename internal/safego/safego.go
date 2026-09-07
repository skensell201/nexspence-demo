// Package safego guards goroutines against an unrecovered panic taking down
// the whole process. Go terminates the entire program on any goroutine panic
// that reaches the top of its stack unrecovered — not just the goroutine that
// panicked — so a single bad input reaching any background job (a malformed
// upstream response, an unexpected shape from a scanner, a nil dereference in
// a cron job body) would otherwise crash every in-flight request along with
// it, not just the misbehaving job.
package safego

import (
	"runtime/debug"

	"github.com/nexspence-oss/nexspence/internal/logger"
)

// Go runs fn in a new goroutine, recovering any panic so it cannot crash the
// process. Use for detached, one-shot work — a background job, a fire-and-
// forget write — where the goroutine's own failure should be logged, not
// fatal to everyone else.
func Go(log logger.Logger, name string, fn func()) {
	go func() {
		defer Recover(log, name)()
		fn()
	}()
}

// Recover returns a function to defer inside one iteration of a persistent
// loop goroutine (a cron job body, a ticker-driven sampler): it recovers a
// panic from that iteration and logs it, letting the loop's next iteration
// run instead of a single bad iteration taking the whole process down. Unlike
// Go, this does not itself start a goroutine — call it as
// `defer safego.Recover(log, "name")()` from inside the loop body (or an
// inner closure wrapping one iteration, when the loop's own scope can't be
// deferred into directly).
func Recover(log logger.Logger, name string) func() {
	return func() {
		if r := recover(); r != nil {
			if log != nil {
				log.Errorw("recovered panic in goroutine",
					"goroutine", name, "panic", r, "stack", string(debug.Stack()))
			}
		}
	}
}
