package server

import (
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// A signed-in session that keeps asking for hidden folders is probing for
// the server's keys: legitimate apps never see those folders in a listing,
// so they never ask for them. After probeLimit such requests within
// probeWindow the session is locked, whatever its role (admin included):
//   - a browser session is signed out;
//   - a LAN (QR) session is ended;
//   - a paired app is unpaired and must be invited again.
const (
	probeLimit  = 5
	probeWindow = 24 * time.Hour
)

type probes struct {
	mu   sync.Mutex
	hits map[string][]time.Time // session key -> hidden-folder requests
}

// sessionKey identifies the session a request belongs to ("" if none).
func sessionKey(who Who) string {
	switch {
	case who.AppID != "":
		return "app:" + who.AppID
	case who.DeviceID != "":
		return "web:" + who.DeviceID
	}
	return ""
}

// record counts one hidden-folder request and reports how many the session
// made within probeWindow.
func (p *probes) record(key string, now time.Time) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.hits == nil {
		p.hits = map[string][]time.Time{}
	}
	for k, ts := range p.hits { // drop old entries so the map stays small
		if len(ts) > 0 && now.Sub(ts[len(ts)-1]) >= probeWindow {
			delete(p.hits, k)
		}
	}
	kept := p.hits[key][:0]
	for _, t := range p.hits[key] {
		if now.Sub(t) < probeWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	p.hits[key] = kept
	return len(kept)
}

func (p *probes) forget(key string) {
	p.mu.Lock()
	delete(p.hits, key)
	p.mu.Unlock()
}

// probesHidden reports whether the request targets a hidden folder, through
// its path or (COPY/MOVE) its Destination.
func (h *handler) probesHidden(r *http.Request) bool {
	if h.hide == nil {
		return false
	}
	if !strings.HasPrefix(r.URL.Path, "/__nasfone/") && h.hidden(r.URL.Path) {
		return true
	}
	if d := r.Header.Get("Destination"); d != "" {
		if u, err := url.Parse(d); err == nil && h.hidden(u.Path) {
			return true
		}
	}
	return false
}

// refuseHidden answers a request for a hidden folder: 404 while the session
// is under the limit, then it locks the session and answers 403.
func (h *handler) refuseHidden(w http.ResponseWriter, r *http.Request, who Who) {
	key := sessionKey(who)
	n := 0
	if key != "" {
		n = h.probes.record(key, time.Now())
	}
	h.opt.Logf("Chặn truy cập thư mục ẩn: %s %s từ %s (%s, %s) — lần %d/%d", r.Method, r.URL.Path, clientIP(r), who.Name, who.Role, n, probeLimit)
	if n < probeLimit {
		http.NotFound(w, r)
		return
	}
	h.probes.forget(key)
	h.lockSession(who)
	h.opt.Logf("Đã khóa phiên %s (%s) vì dò thư mục ẩn %d lần", who.Name, who.Role, probeLimit)
	// Drop the browser's cookies too, so it lands on the sign-in page.
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	http.SetCookie(w, &http.Cookie{Name: lanCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	http.Error(w, tr(pickLang(r), "err_locked"), http.StatusForbidden)
}

// lockSession ends the session behind who.
func (h *handler) lockSession(who Who) {
	switch {
	case who.AppID != "":
		if h.opt.Pair != nil {
			h.opt.Pair.Revoke(who.AppID)
		}
	case strings.HasPrefix(who.DeviceID, "lan:"):
		if h.opt.LAN != nil {
			h.opt.LAN.Revoke(strings.TrimPrefix(who.DeviceID, "lan:"))
		}
	case who.DeviceID != "":
		if h.opt.Auth != nil {
			h.opt.Auth.Revoke(who.DeviceID)
		}
	}
}
