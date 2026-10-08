package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"nasfone/core/auth"
)

func TestPatchWritesInPlace(t *testing.T) {
	root, do := newTestServer(t)
	f := filepath.Join(root, "big.bin")
	status := func(r *http.Response) int { r.Body.Close(); return r.StatusCode }

	// Pre-size (like CopyFile setting the end of file), then write out of order.
	if s := status(do("PATCH", "/big.bin", map[string]string{hdrSize: "10"}, nil)); s != 204 {
		t.Fatalf("create with size: %d", s)
	}
	if s := status(do("PATCH", "/big.bin", map[string]string{hdrOffset: "5"}, strings.NewReader("WORLD"))); s != 204 {
		t.Fatalf("write at 5: %d", s)
	}
	r := do("PATCH", "/big.bin", map[string]string{hdrOffset: "0", hdrSync: "1"}, strings.NewReader("HELLO"))
	if r.StatusCode != 204 || r.Header.Get(hdrSize) != "10" {
		t.Fatalf("write at 0: %d size %q", r.StatusCode, r.Header.Get(hdrSize))
	}
	r.Body.Close()
	if b, _ := os.ReadFile(f); string(b) != "HELLOWORLD" {
		t.Fatalf("content %q", b)
	}

	// Truncate and set the modification time.
	mt := time.Date(2025, 3, 12, 9, 23, 57, 0, time.UTC)
	if s := status(do("PATCH", "/big.bin", map[string]string{hdrSize: "4", hdrMtime: "1741771437000000000"}, nil)); s != 204 {
		t.Fatalf("truncate: %d", s)
	}
	fi, _ := os.Stat(f)
	if b, _ := os.ReadFile(f); string(b) != "HELL" || !fi.ModTime().Equal(mt) {
		t.Fatalf("after truncate %q mtime %v", b, fi.ModTime())
	}

	// Bad input and unsafe targets are refused.
	for _, c := range []struct {
		path string
		hdr  map[string]string
		want int
	}{
		{"/big.bin", map[string]string{hdrOffset: "-1"}, 400},
		{"/big.bin", map[string]string{hdrOffset: "x"}, 400},
		{"/", map[string]string{hdrOffset: "0"}, 403},
		{"/" + tmpPrefix + "x.part", map[string]string{hdrOffset: "0"}, 403},
		{"/missing/dir.bin", map[string]string{hdrOffset: "0"}, 409},
		{"/nothere.bin", map[string]string{hdrSync: "1"}, 404}, // sync alone never creates
	} {
		if s := status(do("PATCH", c.path, c.hdr, strings.NewReader("x"))); s != c.want {
			t.Errorf("PATCH %s %v: %d, want %d", c.path, c.hdr, s, c.want)
		}
	}
	os.Mkdir(filepath.Join(root, "d"), 0o755)
	if s := status(do("PATCH", "/d", map[string]string{hdrOffset: "0"}, strings.NewReader("x"))); s != 405 {
		t.Errorf("PATCH on a folder: %d", s)
	}
	if _, err := os.Stat(filepath.Join(root, "nothere.bin")); err == nil {
		t.Error("sync-only PATCH created a file")
	}
}

func TestPatchChunkLimit(t *testing.T) {
	_, do := newTestServer(t)
	big := strings.NewReader(strings.Repeat("x", maxPatchBody+1))
	r := do("PATCH", "/a.bin", map[string]string{hdrOffset: "0"}, big)
	r.Body.Close()
	if r.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized chunk: %d", r.StatusCode)
	}
}

func TestPatchNeedsWriteAccess(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hello"), 0o644)
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	srv := httptest.NewServer(WithVia(NewHandler(Options{Root: root, Auth: store}), "Tailnet"))
	defer srv.Close()
	_, userCode, _ := store.CurrentCodes()
	tok, _, _ := store.Redeem(userCode, "Guest", "1.1.1.1", "Tailnet")

	req, _ := http.NewRequest("PATCH", srv.URL+"/a.txt", strings.NewReader("XX"))
	req.Header.Set(hdrOffset, "0")
	req.AddCookie(&http.Cookie{Name: "nasfone_s", Value: tok})
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 403 {
		t.Fatalf("user PATCH: %d", res.StatusCode)
	}
	if b, _ := os.ReadFile(filepath.Join(root, "a.txt")); string(b) != "hello" {
		t.Fatalf("file changed by user role: %q", b)
	}
	// Unauthenticated requests are refused too.
	req, _ = http.NewRequest("PATCH", srv.URL+"/a.txt", strings.NewReader("XX"))
	req.Header.Set(hdrOffset, "0")
	if res, err = http.DefaultClient.Do(req); err == nil {
		res.Body.Close()
		if res.StatusCode != 401 {
			t.Fatalf("anonymous PATCH: %d", res.StatusCode)
		}
	}
}
