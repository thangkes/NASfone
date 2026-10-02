// Package update finds newer NASfone releases on GitHub and downloads their
// files, checked against the SHA256SUMS.txt published with each release.
package update

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
)

// Repo is where official builds are published.
const Repo = "thangkes/NASfone"

const sumsName = "SHA256SUMS.txt"

// Release is a newer version with the one file this app needs.
type Release struct {
	Version   string `json:"version"` // "0.2.0"
	Notes     string `json:"notes"`
	PageURL   string `json:"pageURL"`
	AssetName string `json:"assetName"`
	AssetURL  string `json:"assetURL"`
	Size      int64  `json:"size"`
	SumsURL   string `json:"sumsURL"`
}

type ghRelease struct {
	TagName string `json:"tag_name"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Draft   bool   `json:"draft"`
	Assets  []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
		Size int64  `json:"size"`
	} `json:"assets"`
}

// Check returns the newest release above current that has an asset accepted
// by match, or nil when this build is up to date. Development builds
// (current not a version number) never update.
func Check(ctx context.Context, hc *http.Client, current string, match func(name string) bool) (*Release, error) {
	cur, ok := parse(current)
	if !ok {
		return nil, nil
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", "https://api.github.com/repos/"+Repo+"/releases?per_page=15", nil)
	req.Header.Set("Accept", "application/vnd.github+json")
	res, err := hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub: %s", res.Status)
	}
	var list []ghRelease
	if err := json.NewDecoder(io.LimitReader(res.Body, 4<<20)).Decode(&list); err != nil {
		return nil, err
	}
	var best *Release
	var bestV [3]int
	for _, r := range list {
		v, ok := parse(r.TagName)
		if r.Draft || !ok || !less(cur, v) || (best != nil && !less(bestV, v)) {
			continue
		}
		var rel Release
		for _, a := range r.Assets {
			switch {
			case a.Name == sumsName:
				rel.SumsURL = a.URL
			case rel.AssetURL == "" && match(a.Name):
				rel.AssetName, rel.AssetURL, rel.Size = a.Name, a.URL, a.Size
			}
		}
		if rel.AssetURL == "" || rel.SumsURL == "" {
			continue // without checksums we do not install anything
		}
		rel.Version = fmt.Sprintf("%d.%d.%d", v[0], v[1], v[2])
		rel.Notes, rel.PageURL = r.Body, r.HTMLURL
		best, bestV = &rel, v
	}
	return best, nil
}

// Download saves the release asset to dest after checking its SHA-256
// against the release's SHA256SUMS.txt. A partial or mismatching file is removed.
func Download(ctx context.Context, hc *http.Client, r *Release, dest string) error {
	want, err := expectedSum(ctx, hc, r)
	if err != nil {
		return err
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", r.AssetURL, nil)
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("download: %s", res.Status)
	}
	tmp := dest + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), io.LimitReader(res.Body, 512<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil && hex.EncodeToString(h.Sum(nil)) != want {
		err = errors.New("checksum mismatch: the downloaded file is not the published one")
	}
	if err != nil {
		os.Remove(tmp)
		return err
	}
	os.Remove(dest)
	return os.Rename(tmp, dest)
}

func expectedSum(ctx context.Context, hc *http.Client, r *Release) (string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", r.SumsURL, nil)
	res, err := hc.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksums: %s", res.Status)
	}
	sc := bufio.NewScanner(io.LimitReader(res.Body, 64<<10))
	for sc.Scan() {
		// "<hex>  <name>" (sha256sum format; "*" marks binary mode)
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == r.AssetName && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", fmt.Errorf("no checksum published for %s", r.AssetName)
}

// Newer reports whether version b is above a ("0.1.0" < "v0.2.0").
func Newer(a, b string) bool {
	va, ok1 := parse(a)
	vb, ok2 := parse(b)
	return ok1 && ok2 && less(va, vb)
}

func parse(s string) ([3]int, bool) {
	var v [3]int
	s = strings.TrimPrefix(strings.TrimSpace(s), "v")
	if i := strings.IndexAny(s, "-+"); i >= 0 {
		s = s[:i]
	}
	parts := strings.Split(s, ".")
	if len(parts) != 3 {
		return v, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return v, false
		}
		v[i] = n
	}
	return v, true
}

func less(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] < b[i]
		}
	}
	return false
}
