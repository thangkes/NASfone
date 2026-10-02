package mobileclient

import (
	"context"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	"nasfone/core/client"
)

// Entry is one file or folder in a listing.
type Entry struct {
	Name  string `json:"name"`
	Path  string `json:"path"`
	Dir   bool   `json:"dir"`
	Size  int64  `json:"size"`
	MTime int64  `json:"mtime"` // unix ms
	Mime  string `json:"mime"`
}

type multistatus struct {
	Responses []struct {
		Href  string `xml:"href"`
		Props []struct {
			Prop struct {
				Length   string `xml:"getcontentlength"`
				Modified string `xml:"getlastmodified"`
				Type     string `xml:"getcontenttype"`
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

func cleanPath(p string) string {
	p = path.Clean("/" + p)
	return p
}

func (s *Session) propfind(p, depth, body string) (*multistatus, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.do(ctx, "PROPFIND", p, []byte(body), map[string]string{"Depth": depth, "Content-Type": "application/xml"}, true)
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

const propAll = `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop>` +
	`<D:resourcetype/><D:getcontentlength/><D:getlastmodified/><D:getcontenttype/></D:prop></D:propfind>`

func entriesOf(ms *multistatus) []Entry {
	var out []Entry
	for _, r := range ms.Responses {
		href := r.Href
		if u, err := url.Parse(href); err == nil {
			href = u.Path // absolute hrefs carry scheme://host
		}
		p := cleanPath(href)
		e := Entry{Path: p, Name: path.Base(p)}
		for _, ps := range r.Props {
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
				e.MTime = t.UnixMilli()
			}
			if pr.Type != "" {
				e.Mime = pr.Type
			}
		}
		if !e.Dir && e.Mime == "" {
			e.Mime = mime.TypeByExtension(strings.ToLower(path.Ext(e.Name)))
		}
		if !e.Dir && e.Mime == "" {
			e.Mime = "application/octet-stream"
		}
		out = append(out, e)
	}
	return out
}

// List returns the folder's children as a JSON array of Entry, folders
// first, then by name. Hidden upload temp files are left out.
func (s *Session) List(dir string) (string, error) {
	dir = cleanPath(dir)
	ms, err := s.propfind(dir, "1", propAll)
	if err != nil {
		return "", wrap(err)
	}
	var out []Entry
	for _, e := range entriesOf(ms) {
		if e.Path == dir || strings.HasPrefix(e.Name, ".nasfone-upload-") {
			continue
		}
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	if out == nil {
		out = []Entry{}
	}
	b, _ := json.Marshal(out)
	return string(b), nil
}

// Stat returns one Entry as JSON, or an error starting with "notfound:".
func (s *Session) Stat(p string) (string, error) {
	p = cleanPath(p)
	ms, err := s.propfind(p, "0", propAll)
	if err != nil {
		var he *client.HTTPError
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			return "", fmt.Errorf("notfound: %s", p)
		}
		return "", wrap(err)
	}
	es := entriesOf(ms)
	if len(es) == 0 {
		return "", fmt.Errorf("notfound: %s", p)
	}
	es[0].Path = p
	if p == "/" {
		es[0].Name = "/"
	}
	b, _ := json.Marshal(es[0])
	return string(b), nil
}

// Quota returns {"avail","used"} in bytes for the shared folder.
func (s *Session) Quota() (string, error) {
	ms, err := s.propfind("/", "0", `<?xml version="1.0" encoding="utf-8"?><D:propfind xmlns:D="DAV:"><D:prop>`+
		`<D:quota-available-bytes/><D:quota-used-bytes/></D:prop></D:propfind>`)
	if err != nil {
		return "", wrap(err)
	}
	var avail, used int64 = -1, -1
	for _, r := range ms.Responses {
		for _, ps := range r.Props {
			if n, err := strconv.ParseInt(ps.Prop.Avail, 10, 64); err == nil {
				avail = n
			}
			if n, err := strconv.ParseInt(ps.Prop.Used, 10, 64); err == nil {
				used = n
			}
		}
	}
	b, _ := json.Marshal(map[string]int64{"avail": avail, "used": used})
	return string(b), nil
}

// Download streams a file into fd (a pipe or file the app opened for
// writing) and closes fd. progress may be nil.
func (s *Session) Download(p string, fd int, progress Progress) error {
	f := os.NewFile(uintptr(fd), "download")
	defer f.Close()
	if err := s.connect(false); err != nil {
		return wrap(err)
	}
	ctx := context.Background()
	res, err := s.send(streamClient, ctx, "GET", cleanPath(p), nil, nil)
	if err != nil || res.StatusCode == http.StatusUnauthorized {
		if res != nil {
			res.Body.Close()
		}
		if cerr := s.connect(true); cerr != nil {
			return wrap(cerr)
		}
		res, err = s.send(streamClient, ctx, "GET", cleanPath(p), nil, nil)
	}
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return statusErr(res)
	}
	_, err = io.Copy(f, &progressReader{r: res.Body, total: res.ContentLength, p: progress})
	return err
}

// Upload modes when the target already exists.
const (
	ModeOverwrite = "overwrite" // replace it
	ModeKeepBoth  = "keep"      // store as "name (1).ext"
	ModeSkip      = "skip"      // leave it, upload nothing
	ModeFail      = "fail"      // return an error starting with "exists:"
)

// Upload streams fd (opened for reading; size may be -1) to the server path
// and closes fd. It returns the path actually written, or "" when skipped.
func (s *Session) Upload(p string, fd int, size int64, mode string, progress Progress) (string, error) {
	f := os.NewFile(uintptr(fd), "upload")
	defer f.Close()
	p = cleanPath(p)
	if !s.CanWrite() {
		if err := s.connect(false); err != nil {
			return "", wrap(err)
		}
		if !s.CanWrite() {
			return "", errors.New("readonly: this device has view-only access")
		}
	}
	if mode == ModeKeepBoth {
		alt, err := s.freeName(p)
		if err != nil {
			return "", err
		}
		p = alt
	}
	hdr := map[string]string{"Content-Type": "application/octet-stream"}
	if mode != ModeOverwrite {
		hdr["If-None-Match"] = "*"
	}
	if err := s.connect(false); err != nil {
		return "", wrap(err)
	}
	req, err := s.uploadRequest(p, &progressReader{r: f, total: size, p: progress}, size, hdr)
	if err != nil {
		return "", err
	}
	res, err := streamClient.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	switch {
	case res.StatusCode == http.StatusPreconditionFailed && mode == ModeSkip:
		return "", nil
	case res.StatusCode == http.StatusPreconditionFailed:
		return "", fmt.Errorf("exists: %s", p)
	case res.StatusCode >= 300:
		return "", statusErr(res)
	}
	return p, nil
}

func (s *Session) uploadRequest(p string, body io.Reader, size int64, hdr map[string]string) (*http.Request, error) {
	s.mu.Lock()
	base, tok := s.base, s.token
	s.mu.Unlock()
	req, err := http.NewRequest("PUT", base+escapePath(p), body)
	if err != nil {
		return nil, err
	}
	if size >= 0 {
		req.ContentLength = size
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	return req, nil
}

// freeName asks the server for a "keep both" name if p exists.
func (s *Session) freeName(p string) (string, error) {
	dir, name := path.Split(p)
	body, _ := json.Marshal(map[string]any{"base": dir, "paths": []string{name}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	res, err := s.do(ctx, "POST", "/__nasfone/conflicts", body, map[string]string{"Content-Type": "application/json"}, true)
	if err != nil {
		return "", wrap(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return "", statusErr(res)
	}
	var r struct {
		Conflicts map[string]string `json:"conflicts"`
	}
	json.NewDecoder(res.Body).Decode(&r)
	if alt, ok := r.Conflicts[name]; ok {
		return cleanPath(dir + alt), nil
	}
	return p, nil
}

func (s *Session) simple(method, p string, hdr map[string]string, ok ...int) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	res, err := s.do(ctx, method, cleanPath(p), nil, hdr, true)
	if err != nil {
		return wrap(err)
	}
	defer res.Body.Close()
	for _, c := range ok {
		if res.StatusCode == c {
			return nil
		}
	}
	return statusErr(res)
}

// Mkdir creates a folder.
func (s *Session) Mkdir(p string) error {
	return s.simple("MKCOL", p, nil, http.StatusCreated)
}

// Delete removes a file or a folder with everything in it.
func (s *Session) Delete(p string) error {
	return s.simple("DELETE", p, nil, http.StatusNoContent, http.StatusOK)
}

// Move renames or moves p to dest (fails if dest exists).
func (s *Session) Move(p, dest string) error {
	s.mu.Lock()
	base := s.base
	s.mu.Unlock()
	if base == "" {
		if err := s.connect(false); err != nil {
			return wrap(err)
		}
		s.mu.Lock()
		base = s.base
		s.mu.Unlock()
	}
	return s.simple("MOVE", p, map[string]string{"Destination": base + escapePath(cleanPath(dest)), "Overwrite": "F"},
		http.StatusCreated, http.StatusNoContent)
}

type progressReader struct {
	r      io.Reader
	total  int64
	done   int64
	p      Progress
	lastAt time.Time
}

func (pr *progressReader) Read(b []byte) (int, error) {
	n, err := pr.r.Read(b)
	pr.done += int64(n)
	if pr.p != nil && (time.Since(pr.lastAt) > 200*time.Millisecond || err == io.EOF) {
		pr.lastAt = time.Now()
		pr.p.OnProgress(pr.done, pr.total)
	}
	return n, err
}
