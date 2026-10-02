package server

import (
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"rsc.io/qr"
)

// LAN sign-in by QR code (browsers on the same local network).
//
// The login page served on the LAN listener asks for a one-time ticket and
// shows it as a QR code. The phone app scans it and, after the owner
// confirms, the browser that holds the ticket gets a LAN session:
//   - always the read-only user role;
//   - kept only in memory (never saved, gone when the server restarts);
//   - bound to the browser's IP and valid only on the LAN listener;
//   - ends after LANIdleTTL without any traffic, or when the server's own
//     local IP changes (RevokeAll from the host).
const (
	lanStartPath = "/__nasfone/lan/start"
	lanWaitPath  = "/__nasfone/lan/wait"
	lanCookie    = "nasfone_lan"
	// LANPrefix starts the text encoded in the QR code.
	LANPrefix    = "nasfone-lan1:"
	lanTicketTTL = 3 * time.Minute
	// LANIdleTTL ends a LAN session after this long without traffic.
	LANIdleTTL = time.Hour

	lanMaxTicketsPerIP = 5
	lanMaxTickets      = 64
)

// ViaLAN is the listener label (WithVia) for the local-network listener.
const ViaLAN = "LAN"

var (
	ErrLANTicket  = errors.New("this QR code is unknown or expired, show a new one in the browser")
	ErrLANTooMany = errors.New("too many pending QR codes")
)

type lanTicket struct {
	ip, ua  string
	created time.Time
	token   string // set once approved; handed to the browser on its next poll
}

type lanSession struct {
	id, ip, ua    string
	created, seen time.Time
	active        int // requests in flight (a long download counts as traffic)
}

// LANSession describes a LAN session for the phone's list.
type LANSession struct {
	ID       string    `json:"id"`
	IP       string    `json:"ip"`
	Agent    string    `json:"agent"`
	Created  time.Time `json:"created"`
	LastSeen time.Time `json:"lastSeen"`
	Expires  time.Time `json:"expires"`
}

// LAN holds pending QR tickets and live LAN sessions (in memory only).
type LAN struct {
	Now  func() time.Time
	Logf func(format string, args ...any)

	mu       sync.Mutex
	tickets  map[string]*lanTicket  // ticket -> pending sign-in
	sessions map[string]*lanSession // token -> session
}

func NewLAN() *LAN {
	return &LAN{
		Now:      time.Now,
		Logf:     func(string, ...any) {},
		tickets:  map[string]*lanTicket{},
		sessions: map[string]*lanSession{},
	}
}

func randomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return strings.ToLower(strings.TrimRight(base32.StdEncoding.EncodeToString(b), "="))
}

// gc drops expired tickets and idle sessions. Callers hold l.mu.
func (l *LAN) gc(now time.Time) {
	for k, t := range l.tickets {
		if now.Sub(t.created) > lanTicketTTL {
			delete(l.tickets, k)
		}
	}
	for k, s := range l.sessions {
		if s.active == 0 && now.Sub(s.seen) > LANIdleTTL {
			delete(l.sessions, k)
			l.Logf("Phiên LAN %s (%s) hết hạn sau 1 giờ không dùng", s.id, s.ip)
		}
	}
}

func (l *LAN) newTicket(ip, ua string) (string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.gc(now)
	perIP := 0
	for _, t := range l.tickets {
		if t.ip == ip {
			perIP++
		}
	}
	if perIP >= lanMaxTicketsPerIP || len(l.tickets) >= lanMaxTickets {
		return "", ErrLANTooMany
	}
	id := randomID(16)
	l.tickets[id] = &lanTicket{ip: ip, ua: ua, created: now}
	return id, nil
}

func parseLANQR(text string) (string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, LANPrefix) {
		return "", false
	}
	return strings.TrimPrefix(text, LANPrefix), true
}

// TicketInfo returns who is asking (browser IP and user agent) so the phone
// can show it before approving.
func (l *LAN) TicketInfo(qrText string) (ip, agent string, err error) {
	id, ok := parseLANQR(qrText)
	if !ok {
		return "", "", ErrLANTicket
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(l.Now())
	t := l.tickets[id]
	if t == nil || t.token != "" {
		return "", "", ErrLANTicket
	}
	return t.ip, t.ua, nil
}

// Approve turns a scanned ticket into a session; the browser picks it up on
// its next poll. Each ticket works once.
func (l *LAN) Approve(qrText string) (LANSession, error) {
	id, ok := parseLANQR(qrText)
	if !ok {
		return LANSession{}, ErrLANTicket
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.gc(now)
	t := l.tickets[id]
	if t == nil || t.token != "" {
		return LANSession{}, ErrLANTicket
	}
	t.token = randomID(32)
	s := &lanSession{id: randomID(5), ip: t.ip, ua: t.ua, created: now, seen: now}
	l.sessions[t.token] = s
	l.Logf("Đã cho phép phiên LAN %s cho %s (chỉ xem)", s.id, s.ip)
	return s.info(), nil
}

// claim hands the session token to the browser that holds an approved
// ticket. pending is true while the phone has not scanned it yet.
func (l *LAN) claim(ticket, ip string) (token string, pending bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(l.Now())
	t := l.tickets[ticket]
	if t == nil || t.ip != ip {
		return "", false
	}
	if t.token == "" {
		return "", true
	}
	delete(l.tickets, ticket)
	return t.token, false
}

// begin authenticates a LAN cookie and marks a request in flight; the
// returned func ends it. ok is false for unknown, idle or foreign sessions.
func (l *LAN) begin(token, ip string) (s LANSession, end func(), ok bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.Now()
	l.gc(now)
	ss := l.sessions[token]
	if ss == nil || ss.ip != ip {
		return LANSession{}, nil, false
	}
	ss.active++
	ss.seen = now
	return ss.info(), func() {
		l.mu.Lock()
		ss.active--
		ss.seen = l.Now()
		l.mu.Unlock()
	}, true
}

func (l *LAN) logout(token string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.sessions, token)
}

func (s *lanSession) info() LANSession {
	return LANSession{ID: s.id, IP: s.ip, Agent: s.ua, Created: s.created, LastSeen: s.seen, Expires: s.seen.Add(LANIdleTTL)}
}

// Sessions lists live LAN sessions, newest first.
func (l *LAN) Sessions() []LANSession {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.gc(l.Now())
	out := []LANSession{}
	for _, s := range l.sessions {
		out = append(out, s.info())
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// Revoke ends one LAN session by ID.
func (l *LAN) Revoke(id string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	for k, s := range l.sessions {
		if s.id == id {
			delete(l.sessions, k)
			return true
		}
	}
	return false
}

// RevokeAll ends every LAN session and pending ticket (e.g. the local IP changed).
func (l *LAN) RevokeAll() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := len(l.sessions)
	l.sessions = map[string]*lanSession{}
	l.tickets = map[string]*lanTicket{}
	return n
}

// ---- HTTP -------------------------------------------------------------------

// isPrivateIP reports whether ip is on a local network (RFC 1918, link-local,
// IPv6 ULA) — the only peers the LAN listener serves.
func isPrivateIP(s string) bool {
	ip := net.ParseIP(s)
	return ip != nil && (ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLoopback())
}

// lanEndpoints serves the QR sign-in endpoints. It reports whether it handled r.
func (h *handler) lanEndpoints(w http.ResponseWriter, r *http.Request) bool {
	if r.URL.Path != lanStartPath && r.URL.Path != lanWaitPath {
		return false
	}
	if h.opt.LAN == nil || viaFrom(r) != ViaLAN {
		http.NotFound(w, r)
		return true
	}
	w.Header().Set("Cache-Control", "no-store")
	switch r.URL.Path {
	case lanStartPath:
		if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return true
		}
		ua := r.UserAgent()
		if len(ua) > 200 {
			ua = ua[:200]
		}
		ticket, err := h.opt.LAN.newTicket(clientIP(r), ua)
		if err != nil {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": tr(pickLang(r), "err_lan_busy")})
			return true
		}
		c, err := qr.Encode(LANPrefix+ticket, qr.M)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return true
		}
		c.Scale = 6
		writeJSON(w, http.StatusOK, map[string]any{
			"ticket":  ticket,
			"qr":      "data:image/png;base64," + base64.StdEncoding.EncodeToString(c.PNG()),
			"expires": h.opt.LAN.Now().Add(lanTicketTTL).UnixMilli(),
		})
	case lanWaitPath:
		token, pending := h.opt.LAN.claim(r.URL.Query().Get("t"), clientIP(r))
		switch {
		case pending:
			writeJSON(w, http.StatusAccepted, map[string]any{"pending": true})
		case token == "":
			writeJSON(w, http.StatusGone, map[string]any{"error": tr(pickLang(r), "err_lan_expired")})
		default:
			// Browser-session cookie (no Max-Age); plain HTTP on the LAN, so not Secure.
			http.SetCookie(w, &http.Cookie{Name: lanCookie, Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
			writeJSON(w, http.StatusOK, map[string]any{"ok": true})
		}
	}
	return true
}
