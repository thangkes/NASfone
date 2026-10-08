//go:build windows

package drive

import (
	"bytes"
	"context"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/winfsp/cgofuse/fuse"

	"nasfone/core/auth"
	"nasfone/core/client"
	"nasfone/core/pair"
	"nasfone/core/server"
)

// newTestFS runs a real NASfone server in memory, pairs a device with role,
// and returns the drive's file system plus the server's root folder.
func newTestFS(t *testing.T, role auth.Role) (*FS, string) {
	t.Helper()
	root := t.TempDir()
	store, _ := auth.Open(filepath.Join(t.TempDir(), "a.json"))
	pairs, err := pair.Open(filepath.Join(t.TempDir(), "pair"))
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(server.WithVia(server.NewHandler(server.Options{Root: root, Auth: store, Pair: pairs, Logf: t.Logf}), "Tailnet"))
	t.Cleanup(srv.Close)
	inv, _ := pairs.NewInvite(role, srv.URL)
	key, _ := client.GenerateKey()
	cfg, err := client.Pair(context.Background(), http.DefaultClient, inv, key, "test", "windows")
	if err != nil {
		t.Fatal(err)
	}
	r := &Remote{Base: srv.URL, HC: http.DefaultClient, Token: func(ctx context.Context) (string, time.Time, error) {
		tok, err := client.Authenticate(ctx, http.DefaultClient, cfg, key)
		return tok.Value, tok.Expires, err
	}}
	return New(r, role != auth.RoleAdmin), root
}

func TestCopyLikeExplorer(t *testing.T) {
	fs, root := newTestFS(t, auth.RoleAdmin)
	if ok, err := fs.R.SupportsPatch(context.Background()); !ok || err != nil {
		t.Fatalf("SupportsPatch = %v, %v", ok, err)
	}
	data := make([]byte, 10<<20+12345) // 10 MB + a tail: several chunks
	rand.Read(data)

	// CopyFile: create, set the final size, write in 1 MB pieces, set times, close.
	if rc := fs.Mkdir("/ISO", 0o777); rc != 0 {
		t.Fatalf("mkdir %d", rc)
	}
	rc, fh := fs.Create("/ISO/big.iso", fuse.O_RDWR, 0o666)
	if rc != 0 {
		t.Fatalf("create %d", rc)
	}
	if rc := fs.Truncate("/ISO/big.iso", int64(len(data)), fh); rc != 0 {
		t.Fatalf("set size %d", rc)
	}
	for off := 0; off < len(data); off += 1 << 20 {
		end := min(off+1<<20, len(data))
		if n := fs.Write("/ISO/big.iso", data[off:end], int64(off), fh); n != end-off {
			t.Fatalf("write at %d: %d", off, n)
		}
	}
	mt := time.Date(2025, 3, 12, 9, 23, 57, 0, time.UTC)
	if rc := fs.Utimens("/ISO/big.iso", []fuse.Timespec{fuse.NewTimespec(mt), fuse.NewTimespec(mt)}); rc != 0 {
		t.Fatalf("utimens %d", rc)
	}
	// Utimens waited for the pipeline: the data is on the server before close.
	got, _ := os.ReadFile(filepath.Join(root, "ISO", "big.iso"))
	if !bytes.Equal(got, data) {
		t.Fatalf("server copy differs before close (%d of %d bytes)", len(got), len(data))
	}
	fs.Flush("/ISO/big.iso", fh)
	fs.Release("/ISO/big.iso", fh)
	if fi, _ := os.Stat(filepath.Join(root, "ISO", "big.iso")); !fi.ModTime().Equal(mt) {
		t.Fatalf("mtime %v", fi.ModTime())
	}
	// Nothing was written anywhere but the server.
	var st fuse.Stat_t
	if rc := fs.Getattr("/iso/BIG.ISO", &st, ^uint64(0)); rc != 0 || st.Size != int64(len(data)) {
		t.Fatalf("getattr (other case) rc=%d size=%d", rc, st.Size)
	}

	// Read it back: a small header read, then the whole file sequentially.
	rc, rfh := fs.Open("/ISO/big.iso", fuse.O_RDONLY)
	if rc != 0 {
		t.Fatalf("open %d", rc)
	}
	head := make([]byte, 4096)
	if n := fs.Read("/ISO/big.iso", head, 0, rfh); n != 4096 || !bytes.Equal(head, data[:4096]) {
		t.Fatalf("header read %d", n)
	}
	var back []byte
	buf := make([]byte, 1<<20)
	for off := int64(0); ; {
		n := fs.Read("/ISO/big.iso", buf, off, rfh)
		if n < 0 {
			t.Fatalf("read at %d: %d", off, n)
		}
		if n == 0 {
			break
		}
		back = append(back, buf[:n]...)
		off += int64(n)
	}
	fs.Release("/ISO/big.iso", rfh)
	if !bytes.Equal(back, data) {
		t.Fatalf("read back %d bytes, differs", len(back))
	}
}

func TestEditRenameDelete(t *testing.T) {
	fs, root := newTestFS(t, auth.RoleAdmin)
	os.WriteFile(filepath.Join(root, "Doc.txt"), []byte("hello world"), 0o644)

	// Edit in place: overwrite 5 bytes in the middle, then shrink.
	rc, fh := fs.Open("/doc.txt", fuse.O_RDWR) // other letter case
	if rc != 0 {
		t.Fatalf("open %d", rc)
	}
	fs.Write("/doc.txt", []byte("THERE"), 6, fh)
	buf := make([]byte, 64)
	if n := fs.Read("/doc.txt", buf, 0, fh); string(buf[:n]) != "hello THERE" {
		t.Fatalf("read after write %q", buf[:n])
	}
	fs.Truncate("/doc.txt", 5, fh)
	fs.Release("/doc.txt", fh)
	if b, _ := os.ReadFile(filepath.Join(root, "Doc.txt")); string(b) != "hello" {
		t.Fatalf("after edit %q", b)
	}

	// Rename into a new folder, then clean up.
	if rc := fs.Mkdir("/Sub", 0o777); rc != 0 {
		t.Fatalf("mkdir %d", rc)
	}
	if rc := fs.Rename("/doc.txt", "/Sub/renamed.txt"); rc != 0 {
		t.Fatalf("rename %d", rc)
	}
	if _, err := os.Stat(filepath.Join(root, "Sub", "renamed.txt")); err != nil {
		t.Fatal("renamed file missing")
	}
	if rc := fs.Rmdir("/Sub"); rc != -fuse.ENOTEMPTY {
		t.Fatalf("rmdir of a non-empty folder: %d (must refuse)", rc)
	}
	if rc := fs.Unlink("/Sub/renamed.txt"); rc != 0 {
		t.Fatalf("unlink %d", rc)
	}
	if rc := fs.Rmdir("/Sub"); rc != 0 {
		t.Fatalf("rmdir %d", rc)
	}
	var st fuse.Stat_t
	if rc := fs.Getattr("/Sub", &st, ^uint64(0)); rc != -fuse.ENOENT {
		t.Fatalf("deleted folder still visible: %d", rc)
	}
}

func TestWriteErrorFailsTheCopy(t *testing.T) {
	fs, root := newTestFS(t, auth.RoleAdmin)
	rc, fh := fs.Create("/a.bin", fuse.O_RDWR, 0o666)
	if rc != 0 {
		t.Fatalf("create %d", rc)
	}
	// The server stops accepting the file mid-copy (here: its folder is
	// gone): the error must reach Windows, not be swallowed.
	os.Remove(filepath.Join(root, "a.bin"))
	fs.R.Base += "/missing-dir-xyz" // every write now targets a missing parent
	fs.Write("/a.bin", make([]byte, 100), 0, fh)
	if rc := fs.Flush("/a.bin", fh); rc == 0 {
		t.Fatal("flush reported success for a write the server never got")
	}
	if n := fs.Write("/a.bin", []byte("x"), 100, fh); n >= 0 {
		t.Fatal("writes keep succeeding after a failure")
	}
}

func TestUserRoleReadOnly(t *testing.T) {
	fs, root := newTestFS(t, auth.RoleUser)
	os.WriteFile(filepath.Join(root, "a.txt"), []byte("hi"), 0o644)
	if rc, _ := fs.Create("/b.txt", fuse.O_RDWR, 0o666); rc != -fuse.EROFS {
		t.Fatalf("create as user: %d", rc)
	}
	if rc, _ := fs.Open("/a.txt", fuse.O_RDWR); rc != -fuse.EROFS {
		t.Fatalf("open rw as user: %d", rc)
	}
	rc, fh := fs.Open("/a.txt", fuse.O_RDONLY)
	buf := make([]byte, 10)
	if rc != 0 || fs.Read("/a.txt", buf, 0, fh) != 2 {
		t.Fatal("user cannot read")
	}
	if ok, _ := fs.R.SupportsPatch(context.Background()); ok {
		t.Fatal("user role reported as able to write")
	}
}
