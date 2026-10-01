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
	srv := httptest.NewServer(NewHandler(Options{Root: root, Password: "pw"}))
	defer srv.Close()

	do := func(method, path, pass string, body io.Reader) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, body)
		if pass != "" {
			req.SetBasicAuth("u", pass)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	read := func(r *http.Response) string { b, _ := io.ReadAll(r.Body); r.Body.Close(); return string(b) }

	if r := do("GET", "/", "", nil); r.StatusCode != 401 {
		t.Fatalf("no auth: got %d", r.StatusCode)
	}
	if r := do("GET", "/", "wrong", nil); r.StatusCode != 401 {
		t.Fatalf("bad auth: got %d", r.StatusCode)
	}
	r := do("GET", "/", "pw", nil)
	page := read(r)
	if r.StatusCode != 200 || !strings.Contains(page, "a%20b.txt") || !strings.Contains(page, "Ảnh") {
		t.Fatalf("browse: %d\n%s", r.StatusCode, page)
	}
	if r := do("GET", "/a%20b.txt", "pw", nil); read(r) != "hello" {
		t.Fatal("download failed")
	}
	if r := do("PUT", "/%E1%BA%A2nh/x.bin", "pw", strings.NewReader("data")); r.StatusCode != 201 {
		t.Fatalf("put: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "Ảnh", "x.bin")); string(b) != "data" {
		t.Fatal("put content mismatch")
	}
	if r := do("GET", "/../../etc/passwd", "pw", nil); r.StatusCode == 200 && strings.Contains(read(r), "root:") {
		t.Fatal("path traversal")
	}
	if r := do("PROPFIND", "/", "pw", nil); r.StatusCode != 207 {
		t.Fatalf("propfind: %d", r.StatusCode)
	}
	if r := do("GET", "/__pnas/speed?mb=2", "pw", nil); len(read(r)) != 2<<20 {
		t.Fatal("speed download size")
	}
	if r := do("PUT", "/__pnas/speed", "pw", strings.NewReader(strings.Repeat("x", 1000))); !strings.Contains(read(r), `"bytes":1000`) {
		t.Fatal("speed upload")
	}
	if r := do("GET", "/%E1%BA%A2nh", "pw", nil); r.Request.URL.Path != "/Ảnh/" {
		t.Fatalf("dir redirect: %s", r.Request.URL.Path)
	}
}

func TestLoginFlow(t *testing.T) {
	root := t.TempDir()
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Password: "pw", Auth: store}), "LAN"))
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
		req, _ := http.NewRequest("POST", srv.URL+"/__pnas/login", strings.NewReader(`{"code":"`+code+`","name":"Test","remember":true}`))
		req.Header.Set("Content-Type", ct)
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	code, _ := store.NewCode()
	if res := post(code, "text/plain"); res.StatusCode != 415 {
		t.Fatalf("non-json accepted: %d", res.StatusCode)
	}
	if res := post("WRNG-WRNG", "application/json"); res.StatusCode != 401 {
		t.Fatalf("wrong code: %d", res.StatusCode)
	}
	res = post(code, "application/json")
	var sess *http.Cookie
	for _, c := range res.Cookies() {
		if c.Name == "pnas_s" {
			sess = c
		}
	}
	if res.StatusCode != 200 || sess == nil || !sess.HttpOnly || sess.MaxAge <= 0 {
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
