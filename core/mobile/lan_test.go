package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"nasfone/core/server"
)

type fakeHost struct {
	mu     sync.Mutex
	ifaces string
}

func (h *fakeHost) OnStatus(string)        {}
func (h *fakeHost) OnLog(string)           {}
func (h *fakeHost) OnEvent(string, string) {}
func (h *fakeHost) Interfaces() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ifaces
}
func (h *fakeHost) set(s string) { h.mu.Lock(); h.ifaces = s; h.mu.Unlock() }

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

// The LAN listener switches on and off while running, and a local IP change
// or switching off ends LAN sessions.
func TestSetLANLive(t *testing.T) {
	h := &fakeHost{ifaces: "wlan0|3|1500|ubm|192.168.50.20/24,100.101.102.103/32\nlo|1|65536|ul|127.0.0.1/8\n"}
	hostRef = h
	defer func() { hostRef = nil }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	n := &node{host: h, ctx: ctx, cancel: cancel, lan: server.NewLAN()}
	n.handler = server.NewHandler(server.Options{Root: t.TempDir(), LAN: n.lan, Logf: func(string, ...any) {}})

	port := freePort(t)
	get := func() error {
		c := http.Client{Timeout: 2 * time.Second}
		res, err := c.Get(fmt.Sprintf("http://127.0.0.1:%d/__nasfone/login", port))
		if err == nil {
			res.Body.Close()
		}
		return err
	}
	if get() == nil {
		t.Fatal("port open before SetLan")
	}
	if err := n.setLAN(port); err != nil {
		t.Fatal(err)
	}
	if err := get(); err != nil {
		t.Fatalf("LAN listener not reachable: %v", err)
	}
	if u := n.status.LANURLs; len(u) != 1 || u[0] != fmt.Sprintf("http://192.168.50.20:%d", port) {
		t.Fatalf("LANURLs = %v (Tailscale and loopback must be left out)", u)
	}

	// A LAN session survives an unchanged IP and ends when the IP changes.
	res, err := http.Post(fmt.Sprintf("http://127.0.0.1:%d/__nasfone/lan/start", port), "application/json", strings.NewReader("{}"))
	if err != nil {
		t.Fatal(err)
	}
	var j struct{ Ticket string }
	json.NewDecoder(res.Body).Decode(&j)
	res.Body.Close()
	if _, err := n.lan.Approve(server.LANPrefix + j.Ticket); err != nil {
		t.Fatal(err)
	}
	n.checkLAN()
	if len(n.lan.Sessions()) != 1 {
		t.Fatal("session lost without an IP change")
	}
	h.set("wlan0|3|1500|ubm|192.168.1.77/24\n")
	n.checkLAN()
	if u := n.status.LANURLs; len(u) != 1 || !strings.Contains(u[0], "192.168.1.77") {
		t.Fatalf("LANURLs after IP change = %v", u)
	}
	if len(n.lan.Sessions()) != 0 {
		t.Fatal("LAN session kept after the local IP changed")
	}

	if err := n.setLAN(0); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if get() == nil {
		t.Fatal("port still open after switching LAN off")
	}
	if len(n.status.LANURLs) != 0 || !n.lanOff() {
		t.Fatal("status still shows LAN")
	}
}
