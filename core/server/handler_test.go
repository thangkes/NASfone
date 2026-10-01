package server

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"pocketnas/core/auth"
)

func TestHandler(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a b.txt"), []byte("hello"), 0o644)
	os.Mkdir(filepath.Join(root, "Ảnh"), 0o755)
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store}), "Tailnet"))
	defer srv.Close()

	// Sign in once with the current code to get a session token.
	code, _, _ := store.CurrentCodes() // admin code
	token, _, err := store.Redeem(code, "Test", "127.0.0.1", "Tailnet")
	if err != nil {
		t.Fatal(err)
	}

	do := func(method, path string, signedIn bool, body io.Reader) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if signedIn {
			req.AddCookie(&http.Cookie{Name: "pnas_s", Value: token})
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	read := func(r *http.Response) string { b, _ := io.ReadAll(r.Body); r.Body.Close(); return string(b) }

	r := do("GET", "/", false, nil)
	if r.StatusCode != 401 || r.Header.Get("WWW-Authenticate") != "" {
		t.Fatalf("no session: %d, WWW-Authenticate=%q (must not prompt for a password)", r.StatusCode, r.Header.Get("WWW-Authenticate"))
	}
	// Password (HTTP Basic) logins no longer exist.
	req, _ := http.NewRequest("GET", srv.URL+"/", nil)
	req.SetBasicAuth("u", "anything")
	if r, _ := http.DefaultClient.Do(req); r.StatusCode != 401 {
		t.Fatalf("basic auth accepted: %d", r.StatusCode)
	}
	r = do("GET", "/", true, nil)
	page := read(r)
	if r.StatusCode != 200 || !strings.Contains(page, "a%20b.txt") || !strings.Contains(page, "Ảnh") {
		t.Fatalf("browse: %d\n%s", r.StatusCode, page)
	}
	if r := do("GET", "/a%20b.txt", true, nil); read(r) != "hello" {
		t.Fatal("download failed")
	}
	if r := do("PUT", "/%E1%BA%A2nh/x.bin", true, strings.NewReader("data")); r.StatusCode != 201 {
		t.Fatalf("put: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Ảnh", "x.bin")); string(b) != "data" {
		t.Fatal("put content mismatch")
	}
	if r := do("GET", "/../../etc/passwd", true, nil); r.StatusCode == 200 && strings.Contains(read(r), "root:") {
		t.Fatal("path traversal")
	}
	if r := do("PROPFIND", "/", true, nil); r.StatusCode != 207 {
		t.Fatalf("propfind: %d", r.StatusCode)
	}
	if r := do("GET", "/__pnas/speed?mb=2", true, nil); len(read(r)) != 2<<20 {
		t.Fatal("speed download size")
	}
	if r := do("PUT", "/__pnas/speed", true, strings.NewReader(strings.Repeat("x", 1000))); !strings.Contains(read(r), `"bytes":1000`) {
		t.Fatal("speed upload")
	}
	if r := do("GET", "/%E1%BA%A2nh", true, nil); r.Request.URL.Path != "/Ảnh/" {
		t.Fatalf("dir redirect: %s", r.Request.URL.Path)
	}
}

func TestLoginFlow(t *testing.T) {
	root := t.TempDir()
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store}), "LAN"))
	defer srv.Close()
	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	// Browser without session -> login page redirect.
	req, _ := http.NewRequest("GET", srv.URL+"/docs/", nil)
	req.Header.Set("Accept", "text/html")
	res, _ := noRedirect.Do(req)
	if res.StatusCode != 302 || res.Header.Get("Location") != "/__pnas/login?next=%2Fdocs%2F" {
		t.Fatalf("redirect: %d %s", res.StatusCode, res.Header.Get("Location"))
	}
	// Login page is public and next is escaped into JS safely.
	res, _ = http.Get(srv.URL + "/__pnas/login?next=/docs/")
	b, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), `const NEXT = "/docs/"`) {
		t.Fatalf("login page: %d %s", res.StatusCode, b)
	}
	// Open redirect is neutralised.
	res, _ = http.Get(srv.URL + "/__pnas/login?next=//evil.com")
	b, _ = io.ReadAll(res.Body)
	if !strings.Contains(string(b), `const NEXT = "/"`) {
		t.Fatal("open redirect")
	}

	post := func(code string, ct string) *http.Response {
		req, _ := http.NewRequest("POST", srv.URL+"/__pnas/login", strings.NewReader(`{"code":"`+code+`","name":"Test"}`))
		req.Header.Set("Content-Type", ct)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	code, _, _ := store.CurrentCodes() // admin code
	if res := post(code, "text/plain"); res.StatusCode != 415 {
		t.Fatalf("non-json accepted: %d", res.StatusCode)
	}
	wrong := "000000"
	if strings.ReplaceAll(code, " ", "") == wrong {
		wrong = "111111"
	}
	if res := post(wrong, "application/json"); res.StatusCode != 401 {
		t.Fatalf("wrong code: %d", res.StatusCode)
	}
	res = post(code, "application/json")
	var sess *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "pnas_s" {
			sess = c
		}
	}
	// Session cookie only: deleted when the browser closes.
	if res.StatusCode != 200 || sess == nil || !sess.HttpOnly || sess.MaxAge != 0 || !sess.Expires.IsZero() {
		t.Fatalf("login: %d %+v", res.StatusCode, sess)
	}

	// Session cookie grants access and the page shows the device name.
	req, _ = http.NewRequest("GET", srv.URL+"/", nil)
	req.AddCookie(sess)
	res, _ = http.DefaultClient.Do(req)
	b, _ = io.ReadAll(res.Body)
	if res.StatusCode != 200 || !strings.Contains(string(b), "Test") || !strings.Contains(string(b), "Đăng xuất") {
		t.Fatalf("session browse: %d", res.StatusCode)
	}
	if d := store.List(); len(d) != 1 || d[0].Via != "LAN" {
		t.Fatalf("devices: %+v", d)
	}

	// Logout invalidates the session.
	req, _ = http.NewRequest("POST", srv.URL+"/__pnas/logout", nil)
	req.AddCookie(sess)
	http.DefaultClient.Do(req)
	req, _ = http.NewRequest("GET", srv.URL+"/", nil)
	req.AddCookie(sess)
	res, _ = http.DefaultClient.Do(req)
	if res.StatusCode != 401 {
		t.Fatalf("after logout: %d", res.StatusCode)
	}
}
