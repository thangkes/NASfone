package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"pocketnas/core/auth"
)

// failingReader sends some bytes then breaks, like a dropped connection.
type failingReader struct{ sent bool }

func (f *failingReader) Read(p []byte) (int, error) {
	if f.sent {
		return 0, errors.New("connection lost")
	}
	f.sent = true
	return copy(p, "PARTIAL"), nil
}

func newTestServer(t *testing.T) (root string, do func(method, path string, hdr map[string]string, body io.Reader) *http.Response) {
	root = t.TempDir()
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store}), "Tailnet"))
	t.Cleanup(srv.Close)
	code, _ := store.CurrentCode()
	token, _, _ := store.Redeem(code, "T", "127.0.0.1", "Tailnet")
	do = func(method, path string, hdr map[string]string, body io.Reader) *http.Response {
		req, _ := http.NewRequest(method, srv.URL+path, body)
		req.AddCookie(&http.Cookie{Name: "pnas_s", Value: token})
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := http.DefaultClient.Do(req)
		if err != nil {
			return &http.Response{StatusCode: -1, Body: io.NopCloser(strings.NewReader(err.Error()))}
		}
		return res
	}
	return root, do
}

func TestPutAtomicAndNoOverwrite(t *testing.T) {
	root, do := newTestServer(t)
	f := filepath.Join(root, "a.txt")
	os.WriteFile(f, []byte("ORIGINAL"), 0o644)

	// Interrupted upload: original must survive, no temp files left behind.
	do("PUT", "/a.txt", nil, &failingReader{})
	if b, _ := os.ReadFile(f); string(b) != "ORIGINAL" {
		t.Fatalf("original damaged by interrupted upload: %q", b)
	}
	// The client gives up before the server notices the broken body; allow
	// the handler a moment to clean up.
	var left []string
	for i := 0; i < 40; i++ {
		if left, _ = filepath.Glob(filepath.Join(root, tmpPrefix+"*")); len(left) == 0 {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}

	// If-None-Match: * refuses to overwrite.
	if r := do("PUT", "/a.txt", map[string]string{"If-None-Match": "*"}, strings.NewReader("NEW")); r.StatusCode != 412 {
		t.Fatalf("if-none-match: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(f); string(b) != "ORIGINAL" {
		t.Fatal("overwritten despite If-None-Match")
	}
	// Plain PUT overwrites (204), new file is created (201).
	if r := do("PUT", "/a.txt", nil, strings.NewReader("NEW")); r.StatusCode != 204 {
		t.Fatalf("overwrite: %d", r.StatusCode)
	}
	if b, _ := os.ReadFile(f); string(b) != "NEW" {
		t.Fatalf("content %q", b)
	}
	if r := do("PUT", "/b.txt", map[string]string{"If-None-Match": "*"}, strings.NewReader("B")); r.StatusCode != 201 {
		t.Fatalf("create: %d", r.StatusCode)
	}
	// Missing parent folder and temp-name uploads are refused.
	if r := do("PUT", "/nope/c.txt", nil, strings.NewReader("C")); r.StatusCode != 409 {
		t.Fatalf("missing parent: %d", r.StatusCode)
	}
	if r := do("PUT", "/"+tmpPrefix+"x.part", nil, strings.NewReader("x")); r.StatusCode != 403 {
		t.Fatalf("temp name: %d", r.StatusCode)
	}
}

func TestConflicts(t *testing.T) {
	root, do := newTestServer(t)
	os.MkdirAll(filepath.Join(root, "Ảnh", "sub"), 0o755)
	for _, n := range []string{"IMG.jpg", "IMG (1).jpg", "sub/x.png", ".env"} {
		os.WriteFile(filepath.Join(root, "Ảnh", filepath.FromSlash(n)), []byte("x"), 0o644)
	}
	body := `{"base":"/Ảnh/","paths":["IMG.jpg","IMG (2).jpg","new.txt","sub/x.png",".env"]}`
	r := do("POST", "/__pnas/conflicts", map[string]string{"Content-Type": "application/json"}, strings.NewReader(body))
	var got struct{ Conflicts map[string]string }
	json.NewDecoder(r.Body).Decode(&got)
	want := map[string]string{
		"IMG.jpg":   "IMG (3).jpg", // (1) exists on disk, (2) is in this batch
		"sub/x.png": "sub/x (1).png",
		".env":      ".env (1)",
	}
	if len(got.Conflicts) != len(want) {
		t.Fatalf("got %v", got.Conflicts)
	}
	for k, v := range want {
		if got.Conflicts[k] != v {
			t.Errorf("%s -> %q, want %q", k, got.Conflicts[k], v)
		}
	}
}
