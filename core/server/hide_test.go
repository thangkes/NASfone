package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"nasfone/core/auth"
)

// A share of a whole user profile: the server's own data folder inside it is
// hidden from everyone, the rest of the profile stays shared.
func TestHiddenFolders(t *testing.T) {
	root := t.TempDir() // stands for C:\Users\<name>
	roaming := filepath.Join(root, "AppData", "Roaming")
	state := filepath.Join(roaming, "NASfone-Server")
	os.MkdirAll(filepath.Join(state, "pair"), 0o700)
	os.WriteFile(filepath.Join(state, "pair", "identity.pem"), []byte("SECRET KEY"), 0o600)
	os.WriteFile(filepath.Join(roaming, "notes.txt"), []byte("shared"), 0o644)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	notYet := filepath.Join(root, "AppData", "Local", "NASfone") // created later

	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	h := NewHandler(Options{Root: root, Auth: store, Hidden: func() []string { return []string{state, notYet} }})
	srv := httptest.NewServer(WithVia(h, "Funnel"))
	defer srv.Close()

	adminCode, userCode, _ := store.CurrentCodes()
	userTok, _, _ := store.Redeem(userCode, "Guest", "1.1.1.1", "Funnel")
	adminTok, _, _ := store.Redeem(adminCode, "Owner", "1.1.1.1", "Funnel")

	do := func(tok, method, path string, hdr map[string]string, body string) (int, string) {
		h.(*handler).probes = probes{} // this test is about hiding; locking is tested in probe_test.go
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: tok})
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		return res.StatusCode, string(b)
	}

	// The rest of the profile is still shared.
	if code, body := do(userTok, "GET", "/AppData/Roaming/notes.txt", nil, ""); code != 200 || body != "shared" {
		t.Fatalf("sibling file: %d %q", code, body)
	}

	// Reads of the hidden folder fail for both roles, in every spelling.
	reads := []string{
		"/AppData/Roaming/NASfone-Server/pair/identity.pem",
		"/AppData/Roaming/NASfone-Server/",
		"/AppData/Roaming/NASfone-Server",
		"/AppData/Roaming/x/../NASfone-Server/pair/identity.pem",
	}
	if runtime.GOOS == "windows" {
		reads = append(reads,
			"/appdata/ROAMING/nasfone-server/pair/identity.pem",    // letter case
			"/AppData/Roaming/NASfone-Server./pair/identity.pem",   // trailing dot
			"/AppData/Roaming/NASfone-Server%20/pair/identity.pem", // trailing space
		)
	}
	for _, tok := range []string{userTok, adminTok} {
		for _, p := range reads {
			for _, m := range []string{"GET", "HEAD", "PROPFIND"} {
				code, body := do(tok, m, p, map[string]string{"Depth": "1"}, "")
				if code != 404 || strings.Contains(body, "SECRET") || strings.Contains(body, "identity") {
					t.Errorf("%s %s: %d %q", m, p, code, body)
				}
			}
		}
	}

	// Listings leave it out (PROPFIND and the HTML page), siblings stay.
	for _, tok := range []string{userTok, adminTok} {
		_, list := do(tok, "PROPFIND", "/AppData/Roaming/", map[string]string{"Depth": "1"}, "")
		if strings.Contains(list, "NASfone-Server") || !strings.Contains(list, "notes.txt") {
			t.Errorf("PROPFIND listing: %s", list)
		}
		_, page := do(tok, "GET", "/AppData/Roaming/", map[string]string{"Accept": "text/html"}, "")
		if strings.Contains(page, "NASfone-Server") || !strings.Contains(page, "notes.txt") {
			t.Error("HTML listing shows the hidden folder or lacks the sibling")
		}
	}

	// Admin writes into, out of or over the hidden folder are refused.
	writes := []struct {
		method, path string
		hdr          map[string]string
	}{
		{"PUT", "/AppData/Roaming/NASfone-Server/pair/identity.pem", nil},
		{"PUT", "/AppData/Roaming/NASfone-Server/new.txt", nil},
		{"PATCH", "/AppData/Roaming/NASfone-Server/pair/identity.pem", map[string]string{"X-Offset": "0"}},
		{"DELETE", "/AppData/Roaming/NASfone-Server/", nil},
		{"MKCOL", "/AppData/Roaming/NASfone-Server/evil/", nil},
		{"MOVE", "/a.txt", map[string]string{"Destination": srv.URL + "/AppData/Roaming/NASfone-Server/pair/identity.pem", "Overwrite": "T"}},
		{"COPY", "/a.txt", map[string]string{"Destination": srv.URL + "/AppData/Roaming/NASfone-Server/copied.txt"}},
		{"MOVE", "/AppData/Roaming/NASfone-Server/", map[string]string{"Destination": srv.URL + "/stolen/"}},
		{"COPY", "/AppData/Roaming/NASfone-Server/", map[string]string{"Destination": srv.URL + "/stolen/"}},
	}
	for _, wr := range writes {
		if code, _ := do(adminTok, wr.method, wr.path, wr.hdr, "HACKED"); code < 400 {
			t.Errorf("admin %s %s: %d", wr.method, wr.path, code)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(state, "pair", "identity.pem")); string(b) != "SECRET KEY" {
		t.Fatalf("hidden file changed: %q", b)
	}
	for _, p := range []string{filepath.Join(state, "new.txt"), filepath.Join(state, "evil"), filepath.Join(state, "copied.txt"), filepath.Join(root, "stolen")} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s was created", p)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "a.txt")); err != nil {
		t.Error("source of the refused MOVE is gone")
	}

	// A hidden folder that appears later is hidden too (after the refresh).
	os.MkdirAll(notYet, 0o700)
	os.WriteFile(filepath.Join(notYet, "cache.bin"), []byte("x"), 0o600)
	h.(*handler).hide.at = h.(*handler).hide.at.Add(-hiddenRefresh)
	if code, _ := do(adminTok, "GET", "/AppData/Local/NASfone/cache.bin", nil, ""); code != 404 {
		t.Errorf("late folder: %d", code)
	}
}

// A link inside the share that points at a hidden folder does not open it.
func TestHiddenFolderThroughLink(t *testing.T) {
	root := t.TempDir()
	state := filepath.Join(root, "AppData", "Roaming", "NASfone-Server")
	os.MkdirAll(state, 0o700)
	os.WriteFile(filepath.Join(state, "auth.json"), []byte("SECRET"), 0o600)
	link := filepath.Join(root, "shortcut")
	if err := os.Symlink(state, link); err != nil {
		// Symlinks need a privilege on Windows; junctions (what a profile's
		// "Application Data" is) do not.
		if runtime.GOOS != "windows" || exec.Command("cmd", "/c", "mklink", "/J", link, state).Run() != nil {
			t.Skip("cannot create a link here:", err)
		}
	}
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store, Hidden: func() []string { return []string{state} }}), "Funnel"))
	defer srv.Close()
	adminCode, _, _ := store.CurrentCodes()
	tok, _, _ := store.Redeem(adminCode, "Owner", "1.1.1.1", "Funnel")

	req, _ := http.NewRequest("PROPFIND", srv.URL+"/", nil)
	req.Header.Set("Depth", "1")
	req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: tok})
	if res, err := http.DefaultClient.Do(req); err == nil {
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if strings.Contains(string(b), "shortcut") {
			t.Error("listing shows the link to the hidden folder")
		}
	}
	for _, p := range []string{"/shortcut/auth.json", "/shortcut/"} {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: tok})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(res.Body)
		res.Body.Close()
		if res.StatusCode != 404 || strings.Contains(string(b), "SECRET") {
			t.Errorf("GET %s: %d %q", p, res.StatusCode, b)
		}
	}
}

// Files opened in the browser run sandboxed, so an HTML or SVG file on the
// NAS cannot run scripts with the viewer's session.
func TestFilesAreSandboxed(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "evil.html"), []byte("<script>fetch('/a',{method:'DELETE'})</script>"), 0o644)
	os.WriteFile(filepath.Join(root, "evil.svg"), []byte(`<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`), 0o644)
	os.WriteFile(filepath.Join(root, "noext"), []byte("<html><script>alert(1)</script>"), 0o644)
	os.WriteFile(filepath.Join(root, "doc.pdf"), []byte("%PDF-1.4"), 0o644)
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store}), "Funnel"))
	defer srv.Close()
	adminCode, _, _ := store.CurrentCodes()
	tok, _, _ := store.Redeem(adminCode, "Owner", "1.1.1.1", "Funnel")

	get := func(p string) http.Header {
		req, _ := http.NewRequest("GET", srv.URL+p, nil)
		req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: tok})
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != 200 {
			t.Fatalf("GET %s: %d", p, res.StatusCode)
		}
		return res.Header
	}
	for _, p := range []string{"/evil.html", "/evil.svg", "/noext"} {
		hd := get(p)
		if csp := hd.Get("Content-Security-Policy"); !strings.HasPrefix(csp, "sandbox") {
			t.Errorf("%s: CSP %q", p, csp)
		}
		if hd.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: no nosniff", p)
		}
	}
	if hd := get("/doc.pdf"); hd.Get("Content-Security-Policy") != "" || hd.Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("pdf headers: %v", hd)
	}
	// The file browser page itself keeps its scripts.
	if hd := get("/"); hd.Get("Content-Security-Policy") != "" {
		t.Error("browse page is sandboxed")
	}
}
