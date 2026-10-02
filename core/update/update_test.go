package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewer(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"0.1.0", "v0.2.0", true},
		{"0.1.0", "0.1.0", false},
		{"0.2.0", "0.1.9", false},
		{"0.9.0", "0.10.0", true},
		{"dev", "1.0.0", false},
		{"0.1.0", "v0.1.1-beta", true},
		{"0.2.0-beta.1", "0.2.0", true}, // the final release is above its betas
		{"0.2.0", "0.2.0-beta.9", false},
		{"0.2.0-beta.1", "0.2.0-beta.2", true},
		{"0.2.0-beta.2", "0.2.0-beta.10", true},
		{"0.2.0-alpha", "0.2.0-beta", true},
		{"0.2.0-beta", "0.2.0-beta.1", true},
		{"0.1.0", "windows-server-v0.2.0-beta.1", true},
		{"0.2.0-beta.1", "windows-server-v0.2.0", true},
	}
	for _, c := range cases {
		if got := Newer(c.a, c.b); got != c.want {
			t.Errorf("Newer(%q,%q)=%v", c.a, c.b, got)
		}
	}
}

// rewrite sends api.github.com and download requests to a test server.
type rewrite struct{ base string }

func (r rewrite) RoundTrip(req *http.Request) (*http.Response, error) {
	u := *req.URL
	u.Scheme, u.Host = "http", strings.TrimPrefix(r.base, "http://")
	req2 := req.Clone(req.Context())
	req2.URL = &u
	return http.DefaultTransport.RoundTrip(req2)
}

func TestCheckAndDownload(t *testing.T) {
	payload := []byte("new setup bytes")
	sum := sha256.Sum256(payload)
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[
 {"tag_name":"v0.3.0","draft":true,"assets":[]},
 {"tag_name":"v0.2.0","html_url":"page","body":"notes","assets":[
   {"name":"NASfone-Setup-0.2.0.exe","browser_download_url":"%[1]s/setup","size":15},
   {"name":"NASfone-Server-0.2.0.apk","browser_download_url":"%[1]s/apk","size":1},
   {"name":"SHA256SUMS.txt","browser_download_url":"%[1]s/sums","size":1}]},
 {"tag_name":"v0.1.5","assets":[{"name":"NASfone-Setup-0.1.5.exe","browser_download_url":"x"}]}
]`, srvURL)
	})
	mux.HandleFunc("/setup", func(w http.ResponseWriter, r *http.Request) { w.Write(payload) })
	mux.HandleFunc("/sums", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "%s  NASfone-Setup-0.2.0.exe\n%s  NASfone-Server-0.2.0.apk\n", hex.EncodeToString(sum[:]), strings.Repeat("0", 64))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	hc := &http.Client{Transport: rewrite{srv.URL}}
	ctx := context.Background()
	isSetup := func(n string) bool { return strings.HasPrefix(n, "NASfone-Setup-") && strings.HasSuffix(n, ".exe") }

	r, err := Check(ctx, hc, "0.1.0", isSetup)
	if err != nil || r == nil || r.Version != "0.2.0" || r.AssetName != "NASfone-Setup-0.2.0.exe" {
		t.Fatalf("Check: %+v %v", r, err)
	}
	if r2, _ := Check(ctx, hc, "0.2.0", isSetup); r2 != nil {
		t.Fatalf("up to date but got %+v", r2)
	}
	if r2, _ := Check(ctx, hc, "dev", isSetup); r2 != nil {
		t.Fatal("dev build should not update")
	}

	dest := filepath.Join(t.TempDir(), "setup.exe")
	if err := Download(ctx, hc, r, dest); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(dest); string(b) != string(payload) {
		t.Fatal("wrong content")
	}

	// The APK's published sum does not match what the server returns -> rejected, no file left.
	apk, _ := Check(ctx, hc, "0.1.0", func(n string) bool { return strings.HasSuffix(n, ".apk") })
	apk.AssetURL = srv.URL + "/setup"
	bad := filepath.Join(t.TempDir(), "x.apk")
	if err := Download(ctx, hc, apk, bad); err == nil {
		t.Fatal("mismatching checksum accepted")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Fatal("bad download left on disk")
	}
}

// Stable builds are not offered betas; beta builds get newer betas and the
// final release.
func TestCheckBetas(t *testing.T) {
	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/"+Repo+"/releases", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `[
 {"tag_name":"v0.3.0-beta.1","prerelease":true,"assets":[
   {"name":"App-0.3.0-beta.1.exe","browser_download_url":"%[1]s/b"},{"name":"SHA256SUMS.txt","browser_download_url":"%[1]s/s"}]},
 {"tag_name":"v0.2.0","assets":[
   {"name":"App-0.2.0.exe","browser_download_url":"%[1]s/f"},{"name":"SHA256SUMS.txt","browser_download_url":"%[1]s/s"}]}
]`, srvURL)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	srvURL = srv.URL
	hc := &http.Client{Transport: rewrite{srv.URL}}
	any := func(string) bool { return true }
	ctx := context.Background()

	if r, _ := Check(ctx, hc, "0.1.0", any); r == nil || r.Version != "0.2.0" {
		t.Fatalf("stable build: want 0.2.0, got %+v", r)
	}
	if r, _ := Check(ctx, hc, "0.2.0", any); r != nil {
		t.Fatalf("stable build offered a beta: %+v", r)
	}
	if r, _ := Check(ctx, hc, "0.2.0-beta.3", any); r == nil || r.Version != "0.3.0-beta.1" {
		t.Fatalf("beta build: want 0.3.0-beta.1, got %+v", r)
	}
}
