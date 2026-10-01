package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"nasfone/core/auth"
	"nasfone/core/pair"
)

const (
	pairPath      = "/__nasfone/pair"
	challengePath = "/__nasfone/auth/challenge"
	authPath      = "/__nasfone/auth"
	invitePath    = "/__nasfone/invite"
)

// pairingPublic serves the unauthenticated pairing / sign-in endpoints used by
// client apps. It reports whether it handled the request.
func (h *handler) pairingPublic(w http.ResponseWriter, r *http.Request) bool {
	switch r.URL.Path {
	case pairPath, challengePath, authPath:
	default:
		return false
	}
	if h.opt.Pair == nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "ghép đôi chưa được bật"})
		return true
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "POST application/json required"})
		return true
	}
	body := io.LimitReader(r.Body, 16<<10)
	ip, via := clientIP(r), viaFrom(r)
	switch r.URL.Path {
	case pairPath:
		var req pair.PairRequest
		if json.NewDecoder(body).Decode(&req) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "yêu cầu không hợp lệ"})
			return true
		}
		resp, err := h.opt.Pair.Pair(req, ip, via)
		if err != nil {
			pairError(w, err)
			return true
		}
		h.opt.Logf("Đã ghép đôi: %s (%s, %s) qua %s", req.Name, req.Platform, resp.Role, via)
		writeJSON(w, http.StatusOK, resp)
	case challengePath:
		var req struct {
			DeviceID string `json:"deviceId"`
			NonceC   string `json:"nonceC"`
		}
		if json.NewDecoder(body).Decode(&req) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "yêu cầu không hợp lệ"})
			return true
		}
		nonceS, sig, err := h.opt.Pair.Challenge(req.DeviceID, req.NonceC, ip)
		if err != nil {
			pairError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"nonceS": nonceS, "sig": sig})
	case authPath:
		var req struct {
			DeviceID string `json:"deviceId"`
			NonceS   string `json:"nonceS"`
			Sig      string `json:"sig"`
		}
		if json.NewDecoder(body).Decode(&req) != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "yêu cầu không hợp lệ"})
			return true
		}
		token, exp, role, err := h.opt.Pair.Auth(req.DeviceID, req.NonceS, req.Sig, ip, via)
		if err != nil {
			pairError(w, err)
			return true
		}
		writeJSON(w, http.StatusOK, map[string]any{"token": token, "expiresIn": int(time.Until(exp).Seconds()), "role": role})
	}
	return true
}

func pairError(w http.ResponseWriter, err error) {
	status, code := http.StatusUnauthorized, "auth_failed"
	switch {
	case errors.Is(err, pair.ErrRateLimit):
		status, code = http.StatusTooManyRequests, "rate_limited"
	case errors.Is(err, pair.ErrBadRequest), errors.Is(err, pair.ErrBadKey):
		status, code = http.StatusBadRequest, "bad_request"
	case errors.Is(err, pair.ErrUnknown):
		// Clients use this to tell "you were revoked/unpaired" from network trouble.
		code = "unknown_device"
	case errors.Is(err, pair.ErrBadInvite):
		code = "bad_invite"
	}
	time.Sleep(300 * time.Millisecond)
	writeJSON(w, status, map[string]any{"error": err.Error(), "code": code})
}

// invite lets a signed-in admin web session create a one-time pairing invite
// for an app on the same computer (opened through the nasfone:// link).
// Only admins may invite: an invite grants lasting access, which is more than
// a 24-hour web session.
func (h *handler) invite(w http.ResponseWriter, r *http.Request) {
	who := whoFrom(r.Context())
	if h.opt.Pair == nil || !who.CanWrite() {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "POST application/json required", http.StatusBadRequest)
		return
	}
	var req struct {
		Role auth.Role `json:"role"`
	}
	json.NewDecoder(io.LimitReader(r.Body, 1024)).Decode(&req)
	base := ""
	if h.opt.PublicURL != nil {
		base = h.opt.PublicURL()
	}
	if base == "" {
		scheme := "http"
		if r.TLS != nil {
			scheme = "https"
		}
		base = scheme + "://" + r.Host
	}
	inv, exp := h.opt.Pair.NewInvite(req.Role, base)
	h.opt.Logf("%s tạo lời mời ghép đôi (%s)", who.Name, req.Role)
	writeJSON(w, http.StatusOK, map[string]any{
		"invite":  inv,
		"link":    "nasfone://pair?i=" + url.QueryEscape(inv),
		"expires": exp.UnixMilli(),
	})
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	if len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return ""
}
