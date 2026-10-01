//go:build windows

package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"nasfone/core/pair"
)

// localPairAddr is where the tray app listens for invites handed over by the
// NASfone web page on this same computer. Browsers often refuse or silently
// drop nasfone:// links (they must stay tied to a fresh click), but a page
// may talk to 127.0.0.1. Loopback only: other machines cannot reach it.
const localPairAddr = "127.0.0.1:47811"

// serveLocalPairing accepts POST /pair {"invite":"nasfone1:…"} and starts the
// usual pairing flow, which still asks the user to confirm. Any page could
// call this, so nothing happens without that confirmation.
func (a *app) serveLocalPairing() {
	ln, err := net.Listen("tcp", localPairAddr)
	if err != nil {
		return // port taken (another copy?) — the nasfone:// link still works
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/ping", func(w http.ResponseWriter, r *http.Request) {
		if cors(w, r) {
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"app": "NASfone", "version": appVersion})
	})
	mux.HandleFunc("/pair", func(w http.ResponseWriter, r *http.Request) {
		if cors(w, r) {
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Invite string `json:"invite"`
		}
		if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if _, err := pair.ParseInvite(req.Invite); err != nil {
			http.Error(w, "invalid invite", http.StatusBadRequest)
			return
		}
		a.mu.Lock()
		busy := a.busy
		a.busy = true
		a.mu.Unlock()
		if busy {
			http.Error(w, "pairing already in progress", http.StatusConflict)
			return
		}
		go func() {
			ok := pairFromInvite(req.Invite) // shows the confirmation dialog
			a.setBusy(false, "")
			if ok {
				a.reload(true)
			}
		}()
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go srv.Serve(ln)
}

// cors allows calls from web pages (the NASfone page is on its own https
// origin) and answers preflights, including Chrome's private-network check.
// It reports whether the request was a preflight that is now fully answered.
func cors(w http.ResponseWriter, r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin != "" && (strings.HasPrefix(origin, "https://") || strings.HasPrefix(origin, "http://")) {
		w.Header().Set("Access-Control-Allow-Origin", origin)
		w.Header().Set("Vary", "Origin")
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
	w.Header().Set("Access-Control-Allow-Private-Network", "true")
	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return true
	}
	return false
}
