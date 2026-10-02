package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"nasfone/core/auth"
)

const (
	loginPath  = "/__nasfone/login"
	logoutPath = "/__nasfone/logout"
	cookieName = "nasfone_s"
)

//go:embed login.html
var loginHTML string

var loginTmpl = template.Must(template.New("login").Parse(loginHTML))

// Who describes the authenticated caller of a request.
type Who struct {
	Name     string // device name chosen at sign-in
	DeviceID string // browser session ID (empty for paired apps)
	AppID    string // paired app device ID (empty for browsers)
	Role     auth.Role
}

// CanWrite reports whether the caller may change files.
func (w Who) CanWrite() bool { return w.Role == auth.RoleAdmin }

// readOnlyAllowed reports whether a request only reads (allowed for every role).
// Anything not listed here is treated as a write and needs the admin role.
func readOnlyAllowed(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions, "PROPFIND":
		return r.URL.Path != conflictsPath // conflicts only serves uploads
	}
	return false
}

type whoKey struct{}
type viaKey struct{}

func whoFrom(ctx context.Context) Who {
	w, _ := ctx.Value(whoKey{}).(Who)
	return w
}

// WithVia tags requests with the listener they arrived on ("LAN", "Tailnet", "Funnel").
// A connection-level label set with WithConnInfo takes precedence.
func WithVia(h http.Handler, via string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), viaKey{}, via)))
	})
}

type connInfoKey struct{}

type connInfo struct{ via, ip string }

// WithConnInfo records, per connection (http.Server.ConnContext), how a
// client arrived and its real IP when the TCP peer is a relay (Funnel).
func WithConnInfo(ctx context.Context, via, ip string) context.Context {
	return context.WithValue(ctx, connInfoKey{}, connInfo{via: via, ip: ip})
}

func viaFrom(r *http.Request) string {
	if ci, ok := r.Context().Value(connInfoKey{}).(connInfo); ok && ci.via != "" {
		return ci.via
	}
	v, _ := r.Context().Value(viaKey{}).(string)
	return v
}

func clientIP(r *http.Request) string {
	if ci, ok := r.Context().Value(connInfoKey{}).(connInfo); ok && ci.ip != "" {
		return ci.ip
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (h *handler) authenticate(r *http.Request) (Who, bool) {
	if t := bearerToken(r); t != "" && h.opt.Pair != nil {
		if d, ok := h.opt.Pair.CheckToken(t, clientIP(r), viaFrom(r)); ok {
			return Who{Name: d.Name, AppID: d.ID, Role: d.Role}, true
		}
		return Who{}, false
	}
	if c, err := r.Cookie(cookieName); err == nil && h.opt.Auth != nil {
		if d, ok := h.opt.Auth.Check(c.Value, clientIP(r), viaFrom(r)); ok {
			return Who{Name: d.Name, DeviceID: d.ID, Role: d.Role}, true
		}
	}
	return Who{}, false
}

// lanSession authenticates a QR-approved LAN browser (only on the LAN listener).
func (h *handler) lanSession(r *http.Request) (Who, func(), bool) {
	if h.opt.LAN == nil || viaFrom(r) != ViaLAN {
		return Who{}, nil, false
	}
	c, err := r.Cookie(lanCookie)
	if err != nil {
		return Who{}, nil, false
	}
	s, end, ok := h.opt.LAN.begin(c.Value, clientIP(r))
	if !ok {
		return Who{}, nil, false
	}
	return Who{Name: "LAN " + s.IP, DeviceID: "lan:" + s.ID, Role: auth.RoleUser}, end, true
}

type loginRequest struct {
	Code string `json:"code"`
	Name string `json:"name"`
}

func (h *handler) login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		next := r.URL.Query().Get("next")
		if !strings.HasPrefix(next, "/") || strings.HasPrefix(next, "//") {
			next = "/" // only same-site relative redirects
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		lang := pickLang(r)
		lan := h.opt.LAN != nil && viaFrom(r) == ViaLAN
		loginTmpl.Execute(w, map[string]any{"Next": next, "L": lang, "T": dict(lang), "LAN": lan})
	case http.MethodPost:
		// JSON-only: a cross-site HTML form cannot send this content type without CORS.
		if !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
			http.Error(w, "unsupported content type", http.StatusUnsupportedMediaType)
			return
		}
		var req loginRequest
		if err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": tr(pickLang(r), "err_bad_request")})
			return
		}
		if h.opt.Auth == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": tr(pickLang(r), "err_disabled")})
			return
		}
		token, dev, err := h.opt.Auth.Redeem(req.Code, req.Name, clientIP(r), viaFrom(r))
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			lang := pickLang(r)
			var wrong auth.WrongCodeError
			switch {
			case errors.As(err, &wrong):
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": tr(lang, "err_code_wrong"), "left": wrong.Left})
			case errors.Is(err, auth.ErrIPLocked):
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": tr(lang, "err_ip_locked")})
			case errors.Is(err, auth.ErrGlobalLock):
				writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": tr(lang, "err_global_lock")})
			default:
				writeJSON(w, http.StatusUnauthorized, map[string]any{"error": tr(lang, "err_code")})
			}
			return
		}
		// Session cookie: no Max-Age/Expires, so the browser drops it when closed.
		// The server side also ends the session after auth.SessionTTL (24h).
		http.SetCookie(w, &http.Cookie{
			Name:     cookieName,
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   r.TLS != nil,
			SameSite: http.SameSiteLaxMode,
		})
		h.opt.Logf("Đăng nhập: %s qua %s từ %s", dev.Name, dev.Via, dev.LastIP)
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "name": dev.Name})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (h *handler) logout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if c, err := r.Cookie(cookieName); err == nil && h.opt.Auth != nil {
		h.opt.Auth.Logout(c.Value)
	}
	if c, err := r.Cookie(lanCookie); err == nil && h.opt.LAN != nil {
		h.opt.LAN.logout(c.Value)
		http.SetCookie(w, &http.Cookie{Name: lanCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteStrictMode})
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: r.TLS != nil, SameSite: http.SameSiteLaxMode})
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
