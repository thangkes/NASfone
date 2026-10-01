// Package server is the HTTP side of PocketNAS: WebDAV for clients, a small
// HTML file browser for web browsers, and a speed-test endpoint.
package server

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/json"
	"html/template"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/webdav"

	"pocketnas/core/auth"
	"pocketnas/core/pair"
)

//go:embed browse.html
var browseHTML string

var browseTmpl = template.Must(template.New("browse").Funcs(template.FuncMap{
	"size": humanSize,
}).Parse(browseHTML))

// Options configures the handler.
type Options struct {
	Root string      // directory served as "/"
	Auth *auth.Store // browser sessions from one-time login codes
	Pair *pair.Store // paired client apps (bearer tokens)
	// PublicURL, if set and non-empty, is the base URL put in pairing
	// invites (the Funnel https:// address, reachable from anywhere)
	// instead of whatever address the browser happened to use.
	PublicURL func() string
	Logf      func(format string, args ...any)
}

type handler struct {
	opt   Options
	dav   *webdav.Handler
	noise []byte // 1 MiB of random bytes for the download speed test
}

// NewHandler returns the PocketNAS HTTP handler.
func NewHandler(opt Options) http.Handler {
	if opt.Logf == nil {
		opt.Logf = func(string, ...any) {}
	}
	noise := make([]byte, 1<<20)
	rand.Read(noise)
	return &handler{
		opt:   opt,
		noise: noise,
		dav: &webdav.Handler{
			FileSystem: webdav.Dir(opt.Root),
			LockSystem: webdav.NewMemLS(),
			Logger: func(r *http.Request, err error) {
				if err != nil {
					opt.Logf("webdav %s %s: %v", r.Method, r.URL.Path, err)
				}
			},
		},
	}
}

func (h *handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case loginPath:
		h.login(w, r)
		return
	case logoutPath:
		h.logout(w, r)
		return
	}
	if h.pairingPublic(w, r) {
		return
	}
	who, ok := h.authenticate(r)
	if !ok {
		if r.Method == http.MethodGet && strings.Contains(r.Header.Get("Accept"), "text/html") {
			http.Redirect(w, r, loginPath+"?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusFound)
			return
		}
		// No WWW-Authenticate header: there is no password login, so browsers
		// must never show a user/password prompt.
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}
	r = r.WithContext(context.WithValue(r.Context(), whoKey{}, who))
	if !who.CanWrite() && !readOnlyAllowed(r) {
		// Read-only (user) sessions may browse and download, nothing else.
		http.Error(w, "Tài khoản chỉ có quyền xem và tải về.", http.StatusForbidden)
		return
	}
	switch {
	case r.URL.Path == invitePath:
		h.invite(w, r)
		return
	case r.URL.Path == "/__pnas/speed":
		h.speed(w, r)
		return
	case r.URL.Path == conflictsPath:
		h.conflicts(w, r)
		return
	case r.Method == http.MethodPut:
		h.put(w, r)
		return
	}
	if h.quotaPropfind(w, r) {
		return
	}
	if (r.Method == http.MethodGet || r.Method == http.MethodHead) && h.isDir(r.URL.Path) {
		h.browse(w, r)
		return
	}
	h.dav.ServeHTTP(w, r)
}

// localPath maps a URL path to a path under Root. path.Clean on a rooted path
// can never climb above "/", so the result always stays inside Root.
func (h *handler) localPath(urlPath string) string {
	return filepath.Join(h.opt.Root, filepath.FromSlash(path.Clean("/"+urlPath)))
}

func (h *handler) isDir(urlPath string) bool {
	fi, err := os.Stat(h.localPath(urlPath))
	return err == nil && fi.IsDir()
}

type entry struct {
	Name    string
	Href    string
	IsDir   bool
	Size    int64
	ModTime time.Time
}

type crumb struct {
	Name string
	Href string
}

func (h *handler) browse(w http.ResponseWriter, r *http.Request) {
	urlPath := path.Clean("/" + r.URL.Path)
	if !strings.HasSuffix(r.URL.Path, "/") {
		http.Redirect(w, r, escapePath(urlPath+"/"), http.StatusMovedPermanently)
		return
	}
	des, err := os.ReadDir(h.localPath(urlPath))
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	base := escapePath(urlPath)
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	var entries []entry
	for _, de := range des {
		if strings.HasPrefix(de.Name(), tmpPrefix) {
			continue // upload in progress
		}
		fi, err := de.Info()
		if err != nil {
			continue
		}
		e := entry{Name: de.Name(), Href: base + escapeSegment(de.Name()), IsDir: fi.IsDir(), Size: fi.Size(), ModTime: fi.ModTime()}
		if e.IsDir {
			e.Href += "/"
		}
		entries = append(entries, e)
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	crumbs := []crumb{{Name: "PocketNAS", Href: "/"}}
	href := "/"
	for _, seg := range strings.Split(strings.Trim(urlPath, "/"), "/") {
		if seg == "" {
			continue
		}
		href += escapeSegment(seg) + "/"
		crumbs = append(crumbs, crumb{Name: seg, Href: href})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	if err := browseTmpl.Execute(w, map[string]any{
		"Who":     whoFrom(r.Context()),
		"Crumbs":  crumbs,
		"Entries": entries,
		"Base":    base,
	}); err != nil {
		h.opt.Logf("browse template: %v", err)
	}
}

// speed serves GET ?mb=N (download N MiB) and PUT/POST (upload, body discarded).
func (h *handler) speed(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		mb, _ := strconv.Atoi(r.URL.Query().Get("mb"))
		if mb <= 0 || mb > 4096 {
			mb = 100
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.FormatInt(int64(mb)<<20, 10))
		w.Header().Set("Cache-Control", "no-store")
		for i := 0; i < mb; i++ {
			if _, err := w.Write(h.noise); err != nil {
				return
			}
		}
	case http.MethodPut, http.MethodPost:
		start := time.Now()
		n, err := io.Copy(io.Discard, r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]any{"bytes": n, "ms": time.Since(start).Milliseconds()})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, s := range segs {
		segs[i] = escapeSegment(s)
	}
	return strings.Join(segs, "/")
}

func escapeSegment(s string) string {
	return strings.ReplaceAll(templateEscaper.Replace(s), "+", "%2B")
}

// templateEscaper percent-encodes the characters that matter inside a URL path
// segment while leaving UTF-8 letters readable (browsers handle them fine).
var templateEscaper = strings.NewReplacer(
	"%", "%25", " ", "%20", "#", "%23", "?", "%3F", "/", "%2F", "\"", "%22", "'", "%27", "<", "%3C", ">", "%3E", "\\", "%5C",
)

func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return strconv.FormatInt(n, 10) + " B"
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strconv.FormatFloat(float64(n)/float64(div), 'f', 1, 64) + " " + string("KMGTPE"[exp]) + "B"
}
