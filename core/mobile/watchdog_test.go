package mobile

import (
	"context"
	"testing"
	"time"
)

type nopHost struct{}

func (nopHost) OnStatus(string)        {}
func (nopHost) OnLog(string)           {}
func (nopHost) OnEvent(string, string) {}
func (nopHost) Interfaces() string     { return "" }

func TestWatchdogEscalates(t *testing.T) {
	problem, online := "Funnel không truy cập được", true
	var rebinds, restarts int
	defer func(a, b, c, d any) {
		reachProblemFn = a.(func(*node) string)
		internetUpFn = b.(func(context.Context) bool)
		rebindFn = c.(func(*node))
		restartFn = d.(func(*node))
	}(reachProblemFn, internetUpFn, rebindFn, restartFn)
	reachProblemFn = func(*node) string { return problem }
	internetUpFn = func(context.Context) bool { return online }
	rebindFn = func(*node) { rebinds++ }
	restartFn = func(*node) { restarts++ }
	lastRestart = time.Time{}

	n := &node{host: nopHost{}, ctx: context.Background()}
	check := func(wantRebinds, wantRestarts int) {
		t.Helper()
		n.checkHealth()
		if rebinds != wantRebinds || restarts != wantRestarts {
			t.Fatalf("after %d fails: rebinds %d restarts %d, want %d %d", n.wd.fails, rebinds, restarts, wantRebinds, wantRestarts)
		}
	}
	check(0, 0)
	check(1, 0) // ~1 min: rebind
	check(1, 0)
	check(1, 1) // ~2 min: restart
	if n.status.NetProblem == "" {
		t.Fatal("problem not reported to the app")
	}
	check(1, 1) // no second restart within wdRestartGap

	// The phone itself offline: not the server's fault, nothing to repair.
	online = false
	check(1, 1)
	if n.wd.fails != 0 || n.status.NetProblem != "" {
		t.Fatalf("offline phone counted as failure: %d %q", n.wd.fails, n.status.NetProblem)
	}

	// Reachable again: cleared.
	online, problem = true, ""
	check(1, 1)
	if n.status.NetProblem != "" {
		t.Fatal("problem not cleared")
	}
}
