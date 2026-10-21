package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"tailscale.com/ipn"
)

// The watchdog notices when the server is "running" but nobody can reach
// it. The embedded Tailscale engine can lose its coordination and relay
// connections (sleep, a flaky network, the ROM freezing the app for a while)
// while its local state still says Running, so the app showed "connected"
// until the user stopped and started the server by hand. Now the server
// checks itself: when it is unreachable while the phone itself is online, it
// rebinds the engine, and if that does not help it restarts it.
const (
	wdEvery        = 30 * time.Second
	wdRebindAfter  = 2               // failed checks in a row (~1 min) before rebinding
	wdRestartAfter = 4               // and (~2 min) before restarting the engine
	wdRestartGap   = 5 * time.Minute // at most one restart per this time
)

// errStopped means Stop was called while a restart was under way.
var errStopped = errors.New("stopped")

// errRunning: Start was called while a node is running (e.g. the user
// started the server again during an automatic restart).
var errRunning = errors.New("already running")

var (
	epoch       int       // bumped by Stop; a restart that sees it change gives up
	lastRestart time.Time // last automatic restart (the node is replaced by it)
)

type watchdog struct {
	at    time.Time // last check
	busy  bool
	fails int
}

// probeClient checks reachability over the phone's normal network, the way
// a visitor or a paired PC arrives (not through the embedded tailnet).
var probeClient = &http.Client{
	Timeout:       15 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

// maybeCheckHealth runs a check every wdEvery, off the status loop.
func (n *node) maybeCheckHealth() {
	n.mu.Lock()
	due := !n.wd.busy && time.Since(n.wd.at) >= wdEvery
	if due {
		n.wd.busy, n.wd.at = true, time.Now()
	}
	n.mu.Unlock()
	if !due {
		return
	}
	go func() {
		n.checkHealth()
		n.mu.Lock()
		n.wd.busy = false
		n.mu.Unlock()
	}()
}

// Swapped out by tests.
var (
	reachProblemFn = (*node).reachProblem
	internetUpFn   = internetUp
	rebindFn       = (*node).rebind
	restartFn      func(*node) // set in init: restart → start → loop → here
)

func init() { restartFn = func(n *node) { go n.restart() } }

func (n *node) checkHealth() {
	problem := reachProblemFn(n)
	if problem != "" && !internetUpFn(n.ctx) {
		problem = "" // the phone itself is offline: nothing to repair here
	}
	if n.ctx.Err() != nil {
		return
	}
	n.mu.Lock()
	if problem == "" {
		healed := n.wd.fails >= wdRebindAfter
		n.wd.fails = 0
		n.status.NetProblem = ""
		n.mu.Unlock()
		if healed {
			n.logf("Kết nối Tailscale đã ổn định lại")
		}
		n.publish()
		return
	}
	n.wd.fails++
	fails := n.wd.fails
	n.status.NetProblem = problem
	restart := fails >= wdRestartAfter && time.Since(lastRestart) > wdRestartGap
	n.mu.Unlock()
	n.publish()

	n.logf("Kiểm tra kết nối (%d): %s", fails, problem)
	switch {
	case restart:
		mu.Lock()
		lastRestart = time.Now()
		mu.Unlock()
		restartFn(n)
	case fails == wdRebindAfter:
		rebindFn(n)
	}
}

// reachProblem describes why the server looks unreachable, or "".
func (n *node) reachProblem() string {
	lc, err := n.ts.LocalClient()
	if err != nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(n.ctx, 5*time.Second)
	st, err := lc.StatusWithoutPeers(ctx)
	cancel()
	if err != nil || st.BackendState != ipn.Running.String() {
		return "" // starting, signed out…: shown to the user already
	}
	n.mu.Lock()
	funnel := n.status.FunnelURL
	n.mu.Unlock()
	if funnel != "" {
		// The public address goes through Tailscale's relays to this
		// engine: if it answers, everything on the way works.
		if err := headOK(n.ctx, funnel+"/"); err != nil {
			return "Funnel không truy cập được: " + shortErr(err)
		}
		return ""
	}
	for _, h := range st.Health {
		l := strings.ToLower(h)
		if strings.Contains(l, "coordination server") || strings.Contains(l, "derp") {
			return h
		}
	}
	return ""
}

func headOK(ctx context.Context, u string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, u, nil)
	if err != nil {
		return err
	}
	res, err := probeClient.Do(req)
	if err != nil {
		return err
	}
	res.Body.Close()
	return nil // any answer means the server was reached
}

// internetUp reports whether the phone can reach Tailscale's coordination
// server over its normal network.
func internetUp(ctx context.Context) bool {
	return headOK(ctx, "https://controlplane.tailscale.com/") == nil
}

func shortErr(err error) string {
	s := err.Error()
	if r := []rune(s); len(r) > 120 {
		s = string(r[:120]) + "…"
	}
	return s
}

// rebind makes the engine drop its sockets and find a new path.
func (n *node) rebind() {
	lc, err := n.ts.LocalClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
	defer cancel()
	for _, a := range []string{"rebind", "restun"} {
		if err := lc.DebugAction(ctx, a); err != nil {
			n.logf("Nối lại (%s): %v", a, err)
		}
	}
	n.logf("Đã yêu cầu Tailscale nối lại")
}

// restart stops this node and starts a fresh one with the same settings,
// what the user used to do by hand. Pairings and sessions live on disk and
// survive it.
func (n *node) restart() {
	n.mu.Lock()
	cfg, err := json.Marshal(n.cfg) // includes changes made since Start (Funnel, LAN)
	n.mu.Unlock()
	if err != nil {
		return
	}
	mu.Lock()
	if current != n {
		mu.Unlock()
		return
	}
	current = nil
	e := epoch
	mu.Unlock()

	n.logf("Không truy cập được server từ Internet, đang khởi động lại Tailscale…")
	n.shutdown()
	for try := 1; ; try++ {
		time.Sleep(time.Duration(try*try) * 2 * time.Second)
		err := start(string(cfg), n.host, e)
		if err == nil || errors.Is(err, errStopped) || errors.Is(err, errRunning) {
			return
		}
		n.logf("Khởi động lại thất bại (lần %d): %v", try, err)
		b, _ := json.Marshal(Status{Error: fmt.Sprintf("Khởi động lại thất bại: %v", err)})
		n.host.OnStatus(string(b))
		if try == 5 {
			return // the user can still stop and start the server
		}
	}
}
