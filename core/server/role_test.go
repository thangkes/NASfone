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

func TestUserRoleIsReadOnly(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	os.Mkdir(filepath.Join(root, "dir"), 0o755)
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store}), "Funnel"))
	defer srv.Close()

	adminCode, userCode, _ := store.CurrentCodes()
	userTok, dev, err := store.Redeem(userCode, "Guest", "1.1.1.1", "Funnel")
	if err != nil || dev.Role != auth.RoleUser {
		t.Fatalf("user redeem: %v %+v", err, dev)
	}
	adminTok, _, err := store.Redeem(adminCode, "Owner", "1.1.1.1", "Funnel")
	if err != nil {
		t.Fatal(err)
	}

	do := func(tok, method, path string, hdr map[string]string, body string) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		req.AddCookie(&http.Cookie{Name: "pnas_s", Value: tok})
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	read := func(r *http.Response) string { b, _ := io.ReadAll(r.Body); r.Body.Close(); return string(b) }

	// Reads are allowed.
	if r := do(userTok, "GET", "/a.txt", nil, ""); read(r) != "hello" {
		t.Fatal("user cannot download")
	}
	if r := do(userTok, "PROPFIND", "/", map[string]string{"Depth": "1"}, ""); r.StatusCode != 207 {
		t.Fatalf("user propfind: %d", r.StatusCode)
	}
	if r := do(userTok, "GET", "/__pnas/speed?mb=1", nil, ""); r.StatusCode != 200 {
		t.Fatalf("user speed download: %d", r.StatusCode)
	}
	page := read(do(userTok, "GET", "/", nil, ""))
	if strings.Contains(page, "Tải file lên") || strings.Contains(page, `title="Xóa"`) || !strings.Contains(page, "CHỈ XEM") {
		t.Fatal("user page shows write controls or lacks the read-only badge")
	}
	if !strings.Contains(read(do(adminTok, "GET", "/", nil, "")), "Tải file lên") {
		t.Fatal("admin page lacks upload button")
	}

	// Every write is refused for the user and leaves the disk untouched.
	writes := []struct {
		method, path string
		hdr          map[string]string
		body         string
	}{
		{"PUT", "/a.txt", nil, "HACKED"},
		{"PUT", "/new.txt", nil, "x"},
		{"DELETE", "/a.txt", nil, ""},
		{"MKCOL", "/evil/", nil, ""},
		{"MOVE", "/a.txt", map[string]string{"Destination": srv.URL + "/b.txt"}, ""},
		{"COPY", "/a.txt", map[string]string{"Destination": srv.URL + "/c.txt"}, ""},
		{"PROPPATCH", "/a.txt", nil, ""},
		{"LOCK", "/a.txt", nil, ""},
		{"POST", "/__pnas/conflicts", map[string]string{"Content-Type": "application/json"}, `{"base":"/","paths":["a.txt"]}`},
		{"PUT", "/__pnas/speed", nil, "xxxx"},
		{"POST", "/a.txt", nil, ""},
	}
	for _, w := range writes {
		if r := do(userTok, w.method, w.path, w.hdr, w.body); r.StatusCode != http.StatusForbidden {
			t.Errorf("user %s %s: got %d, want 403", w.method, w.path, r.StatusCode)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "hello" {
		t.Fatal("user changed a file")
	}
	for _, p := range []string{"new.txt", "evil", "b.txt", "c.txt"} {
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Fatalf("user created %s", p)
		}
	}

	// The admin can write.
	if r := do(adminTok, "PUT", "/a.txt", nil, "NEW"); r.StatusCode != 204 {
		t.Fatalf("admin put: %d", r.StatusCode)
	}
}
