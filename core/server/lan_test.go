package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nasfone/core/auth"
)

type lanEnv struct {
	t     *testing.T
	h     http.Handler
	lan   *LAN
	clock time.Time
}

func newLANEnv(t *testing.T) *lanEnv {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	e := &lanEnv{t: t, lan: NewLAN(), clock: time.Unix(1_800_000_000, 0)}
	e.lan.Now = func() time.Time { return e.clock }
	e.h = NewHandler(Options{Root: root, Auth: store, LAN: e.lan, Logf: func(string, ...any) {}})
	return e
}

// do sends a request through the given listener label from ip.
func (e *lanEnv) do(via, ip, method, path, cookie string, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = ip + ":40000"
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	if cookie != "" {
		r.Header.Set("Cookie", lanCookie+"="+cookie)
	}
	w := httptest.NewRecorder()
	WithVia(e.h, via).ServeHTTP(w, r)
	return w
}

func (e *lanEnv) signIn(ip string) string {
	w := e.do(ViaLAN, ip, "POST", lanStartPath, "", "{}")
	if w.Code != 200 {
		e.t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var j struct{ Ticket, QR string }
	json.Unmarshal(w.Body.Bytes(), &j)
	if j.Ticket == "" || !strings.HasPrefix(j.QR, "data:image/png;base64,") {
		e.t.Fatalf("bad start reply %s", w.Body)
	}
	if w := e.do(ViaLAN, ip, "GET", lanWaitPath+"?t="+j.Ticket, "", ""); w.Code != http.StatusAccepted {
		e.t.Fatalf("before scan: want 202, got %d", w.Code)
	}
	if gotIP, _, err := e.lan.TicketInfo(LANPrefix + j.Ticket); err != nil || gotIP != ip {
		e.t.Fatalf("TicketInfo: %q %v", gotIP, err)
	}
	if _, err := e.lan.Approve(LANPrefix + j.Ticket); err != nil {
		e.t.Fatal(err)
	}
	if _, err := e.lan.Approve(LANPrefix + j.Ticket); err == nil {
		e.t.Fatal("a ticket was approved twice")
	}
	// Another machine cannot pick up this browser's session.
	if w := e.do(ViaLAN, "192.168.1.99", "GET", lanWaitPath+"?t="+j.Ticket, "", ""); w.Code == 200 {
		e.t.Fatal("session handed to another IP")
	}
	w = e.do(ViaLAN, ip, "GET", lanWaitPath+"?t="+j.Ticket, "", "")
	if w.Code != 200 {
		e.t.Fatalf("after scan: %d", w.Code)
	}
	for _, c := range w.Result().Cookies() {
		if c.Name == lanCookie && c.Value != "" {
			if c.MaxAge != 0 || !c.Expires.IsZero() {
				e.t.Fatal("LAN cookie must be a browser-session cookie")
			}
			return c.Value
		}
	}
	e.t.Fatal("no LAN cookie")
	return ""
}

func TestLANSignIn(t *testing.T) {
	e := newLANEnv(t)
	const ip = "192.168.1.20"
	tok := e.signIn(ip)

	if w := e.do(ViaLAN, ip, "GET", "/a.txt", tok, ""); w.Code != 200 || w.Body.String() != "hello" {
		t.Fatalf("download: %d", w.Code)
	}
	// Always read-only.
	if w := e.do(ViaLAN, ip, "PUT", "/b.txt", tok, ""); w.Code != http.StatusForbidden {
		t.Fatalf("upload with LAN session: %d", w.Code)
	}
	// Bound to the browser's IP and to the LAN listener.
	if w := e.do(ViaLAN, "192.168.1.21", "GET", "/a.txt", tok, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("other IP: %d", w.Code)
	}
	if w := e.do("Tailnet", ip, "GET", "/a.txt", tok, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("LAN cookie accepted on the tailnet: %d", w.Code)
	}
	if w := e.do("Tailnet", ip, "POST", lanStartPath, "", "{}"); w.Code != http.StatusNotFound {
		t.Fatalf("QR sign-in offered off the LAN: %d", w.Code)
	}
	if len(e.lan.Sessions()) != 1 {
		t.Fatal("session not listed")
	}

	// 59 minutes idle: still fine (and the request refreshes it).
	e.clock = e.clock.Add(59 * time.Minute)
	if w := e.do(ViaLAN, ip, "GET", "/a.txt", tok, ""); w.Code != 200 {
		t.Fatalf("after 59 min: %d", w.Code)
	}
	// Over an hour without traffic: gone.
	e.clock = e.clock.Add(61 * time.Minute)
	if w := e.do(ViaLAN, ip, "GET", "/a.txt", tok, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("idle session still valid: %d", w.Code)
	}
}

func TestLANActiveTransferKeepsSession(t *testing.T) {
	e := newLANEnv(t)
	tok := e.signIn("192.168.1.30")
	_, end, ok := e.lan.begin(tok, "192.168.1.30") // a long download in flight
	if !ok {
		t.Fatal("begin")
	}
	e.clock = e.clock.Add(3 * time.Hour)
	if len(e.lan.Sessions()) != 1 {
		t.Fatal("session dropped during a transfer")
	}
	end()
	e.clock = e.clock.Add(61 * time.Minute)
	if len(e.lan.Sessions()) != 0 {
		t.Fatal("session kept after the transfer went idle")
	}
}

func TestLANRevokeAllAndPublicIP(t *testing.T) {
	e := newLANEnv(t)
	tok := e.signIn("10.0.0.5")
	if n := e.lan.RevokeAll(); n != 1 {
		t.Fatalf("RevokeAll=%d", n)
	}
	if w := e.do(ViaLAN, "10.0.0.5", "GET", "/a.txt", tok, ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("revoked session works: %d", w.Code)
	}
	if w := e.do(ViaLAN, "8.8.8.8", "GET", "/", "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("public IP on the LAN listener: %d", w.Code)
	}
	// The login page on the LAN shows the QR block; elsewhere it does not.
	r := httptest.NewRequest("GET", loginPath, nil)
	r.RemoteAddr = "10.0.0.5:1"
	w := httptest.NewRecorder()
	WithVia(e.h, ViaLAN).ServeHTTP(w, r)
	if !strings.Contains(w.Body.String(), `id="lanQR"`) {
		t.Fatal("LAN login page without QR")
	}
	w = httptest.NewRecorder()
	WithVia(e.h, "Tailnet").ServeHTTP(w, r)
	if strings.Contains(w.Body.String(), `id="lanQR"`) {
		t.Fatal("QR shown off the LAN")
	}
}
