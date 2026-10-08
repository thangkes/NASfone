// Package drive is NASfone's own Windows drive: a WinFsp file system whose
// writes go straight into the file on the server (PATCH at an offset) and
// whose reads fetch byte ranges on demand. Nothing is staged on the local
// disk; only a few MB are buffered in RAM while in flight.
package drive

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Entry is one file or folder on the server.
type Entry struct {
	Name  string
	Dir   bool
	Size  int64
	MTime time.Time
}

// Error kinds the file system maps to Windows errors.
var (
	ErrNotFound    = errors.New("not found")
	ErrExists      = errors.New("already exists")
	ErrDenied      = errors.New("access denied")
	ErrNoSpace     = errors.New("no space left on the server")
	ErrUnsupported = errors.New("server does not support direct writes")
)

// Remote talks to one NASfone server.
type Remote struct {
	Base  string // server URL, e.g. https://nasfone.x.ts.net (no trailing slash)
	HC    *http.Client
	Token func(ctx context.Context) (value string, expires time.Time, err error)

	mu     sync.Mutex
	tok    string
	tokExp time.Time
}

func (r *Remote) bearer(ctx context.Context, fresh bool) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !fresh && r.tok != "" && time.Until(r.tokExp) > time.Minute {
		return r.tok, nil
	}
	v, exp, err := r.Token(ctx)
	if err != nil {
		return "", err
	}
	r.tok, r.tokExp = v, exp
	return v, nil
}

func escapePath(p string) string {
	return (&url.URL{Path: path.Clean("/" + p)}).EscapedPath()
}

// do sends one request. The body is a byte slice so it can be resent after a
// token refresh or a dropped connection. Network errors are retried a few
// times: every operation here is idempotent (writes carry their offset).
func (r *Remote) do(ctx context.Context, method, p string, body []byte, hdr map[string]string) (*http.Response, error) {
	var lastErr error
	fresh := false
	for attempt := 0; attempt < 4; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*attempt) * 500 * time.Millisecond):
			}
		}
		tok, err := r.bearer(ctx, fresh)
		if err != nil {
			lastErr = err
			continue
		}
		req, err := http.NewRequestWithContext(ctx, method, r.Base+escapePath(p), bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.ContentLength = int64(len(body))
		req.Header.Set("Authorization", "Bearer "+tok)
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		res, err := r.HC.Do(req)
		if err != nil {
			lastErr = err // network trouble: try again
			if ctx.Err() != nil {
				return nil, err
			}
			continue
		}
		if res.StatusCode == http.StatusUnauthorized && !fresh {
			res.Body.Close()
			fresh = true // token expired or revoked: get a new one once
			attempt--
			continue
		}
		if res.StatusCode == http.StatusBadGateway || res.StatusCode == http.StatusServiceUnavailable || res.StatusCode == http.StatusGatewayTimeout {
			res.Body.Close()
			lastErr = fmt.Errorf("server busy (%d)", res.StatusCode)
			continue
		}
		return res, nil
	}
	return nil, lastErr
}

func statusErr(res *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
	msg := strings.TrimSpace(string(b))
	switch res.StatusCode {
	case http.StatusNotFound, http.StatusConflict: // 409: parent folder missing
		return ErrNotFound
	case http.StatusForbidden, http.StatusUnauthorized:
		return ErrDenied
	case http.StatusPreconditionFailed:
		return ErrExists
	case http.StatusInsufficientStorage:
		return ErrNoSpace
	}
	return fmt.Errorf("server: %d %s", res.StatusCode, msg)
}

type multistatus struct {
	Responses []struct {
		Href  string `xml:"href"`
		Props []struct {
			Prop struct {
				Length   string `xml:"getcontentlength"`
				Modified string `xml:"getlastmodified"`
				Resource struct {
					Collection *struct{} `xml:"collection"`
				} `xml:"resourcetype"`
				Avail string `xml:"quota-available-bytes"`
				Used  string `xml:"quota-used-bytes"`
			} `xml:"prop"`
			Status string `xml:"status"`
		} `xml:"propstat"`
	} `xml:"response"`
}

const propEntry = `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop>` +
	`<D:resourcetype/><D:getcontentlength/><D:getlastmodified/></D:prop></D:propfind>`

const propQuota = `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop>` +
	`<D:quota-available-bytes/><D:quota-used-bytes/></D:prop></D:propfind>`

func (r *Remote) propfind(ctx context.Context, p, depth, body string) (*multistatus, error) {
	res, err := r.do(ctx, "PROPFIND", p, []byte(body), map[string]string{"Depth": depth, "Content-Type": "application/xml"})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusMultiStatus {
		return nil, statusErr(res)
	}
	var ms multistatus
	if err := xml.NewDecoder(io.LimitReader(res.Body, 64<<20)).Decode(&ms); err != nil {
		return nil, err
	}
	return &ms, nil
}

// entries turns a multistatus into (clean path, Entry) pairs.
func entries(ms *multistatus) map[string]Entry {
	out := map[string]Entry{}
	for _, resp := range ms.Responses {
		href := resp.Href
		if u, err := url.Parse(href); err == nil {
			href = u.Path
		}
		p := path.Clean("/" + href)
		e := Entry{Name: path.Base(p)}
		for _, ps := range resp.Props {
			if !strings.Contains(ps.Status, "200") {
				continue
			}
			pr := ps.Prop
			if pr.Resource.Collection != nil {
				e.Dir = true
			}
			if n, err := strconv.ParseInt(pr.Length, 10, 64); err == nil {
				e.Size = n
			}
			if t, err := http.ParseTime(pr.Modified); err == nil {
				e.MTime = t
			}
		}
		out[p] = e
	}
	return out
}

// hidden reports server-side upload temp files, which never show on the drive.
func hidden(name string) bool { return strings.HasPrefix(name, ".nasfone-upload-") }

// Stat returns one entry.
func (r *Remote) Stat(ctx context.Context, p string) (Entry, error) {
	ms, err := r.propfind(ctx, p, "0", propEntry)
	if err != nil {
		return Entry{}, err
	}
	for _, e := range entries(ms) {
		if p == "/" {
			e.Name, e.Dir = "", true
		}
		return e, nil
	}
	return Entry{}, ErrNotFound
}

// List returns a folder's children.
func (r *Remote) List(ctx context.Context, dir string) ([]Entry, error) {
	dir = path.Clean("/" + dir)
	ms, err := r.propfind(ctx, dir, "1", propEntry)
	if err != nil {
		return nil, err
	}
	var out []Entry
	for p, e := range entries(ms) {
		if p == dir || path.Dir(p) != dir || hidden(e.Name) {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}

// Read returns up to n bytes at off (fewer at the end of the file).
func (r *Remote) Read(ctx context.Context, p string, off int64, n int) ([]byte, error) {
	res, err := r.do(ctx, http.MethodGet, p, nil, map[string]string{"Range": fmt.Sprintf("bytes=%d-%d", off, off+int64(n)-1)})
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	switch res.StatusCode {
	case http.StatusPartialContent:
		return io.ReadAll(io.LimitReader(res.Body, int64(n)))
	case http.StatusRequestedRangeNotSatisfiable:
		return nil, nil // at or past the end
	case http.StatusOK: // server ignored Range: skip to off
		if _, err := io.CopyN(io.Discard, res.Body, off); err != nil {
			return nil, nil
		}
		return io.ReadAll(io.LimitReader(res.Body, int64(n)))
	}
	return nil, statusErr(res)
}

// Patch is the direct-write call: data at off (if off >= 0), then size (if
// size >= 0), mtime (if not zero) and a flush to storage (if sync).
func (r *Remote) Patch(ctx context.Context, p string, off int64, data []byte, size int64, mtime time.Time, sync bool) error {
	hdr := map[string]string{}
	if off >= 0 {
		hdr["X-NASfone-Offset"] = strconv.FormatInt(off, 10)
	}
	if size >= 0 {
		hdr["X-NASfone-Size"] = strconv.FormatInt(size, 10)
	}
	if !mtime.IsZero() {
		hdr["X-NASfone-Mtime"] = strconv.FormatInt(mtime.UnixNano(), 10)
	}
	if sync {
		hdr["X-NASfone-Sync"] = "1"
	}
	res, err := r.do(ctx, http.MethodPatch, p, data, hdr)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNoContent || res.StatusCode == http.StatusOK {
		return nil
	}
	return statusErr(res)
}

// SupportsPatch tells a server with the direct-write API (it answers a
// sync-only PATCH on a missing file with 404) from an older one (400/405).
func (r *Remote) SupportsPatch(ctx context.Context) (bool, error) {
	res, err := r.do(ctx, http.MethodPatch, "/.nasfone-probe", nil, map[string]string{"X-NASfone-Sync": "1"})
	if err != nil {
		return false, err
	}
	res.Body.Close()
	switch res.StatusCode {
	case http.StatusNotFound:
		return true, nil
	case http.StatusForbidden:
		return false, ErrDenied // user role: read-only anyway
	}
	return false, nil
}

func (r *Remote) simple(ctx context.Context, method, p string, hdr map[string]string, ok ...int) error {
	res, err := r.do(ctx, method, p, nil, hdr)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	for _, c := range ok {
		if res.StatusCode == c {
			return nil
		}
	}
	if method == "MKCOL" && res.StatusCode == http.StatusMethodNotAllowed {
		return ErrExists
	}
	return statusErr(res)
}

func (r *Remote) Mkdir(ctx context.Context, p string) error {
	return r.simple(ctx, "MKCOL", p, nil, http.StatusCreated, http.StatusOK)
}

func (r *Remote) Delete(ctx context.Context, p string) error {
	return r.simple(ctx, http.MethodDelete, p, nil, http.StatusNoContent, http.StatusOK)
}

func (r *Remote) Move(ctx context.Context, from, to string, overwrite bool) error {
	ow := "F"
	if overwrite {
		ow = "T"
	}
	return r.simple(ctx, "MOVE", from, map[string]string{"Destination": r.Base + escapePath(to), "Overwrite": ow},
		http.StatusCreated, http.StatusNoContent, http.StatusOK)
}

// Quota returns the free and used bytes of the server's storage.
func (r *Remote) Quota(ctx context.Context) (avail, used int64, err error) {
	ms, err := r.propfind(ctx, "/", "0", propQuota)
	if err != nil {
		return 0, 0, err
	}
	for _, resp := range ms.Responses {
		for _, ps := range resp.Props {
			if a, err := strconv.ParseInt(ps.Prop.Avail, 10, 64); err == nil {
				avail = a
			}
			if u, err := strconv.ParseInt(ps.Prop.Used, 10, 64); err == nil {
				used = u
			}
		}
	}
	return avail, used, nil
}
