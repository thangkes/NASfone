package server

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"path"
	"strings"
)

// quotaPropfind answers a Depth: 0 PROPFIND that asks for the RFC 4331 quota
// properties (rclone does this for "about", which sets the drive size shown
// by Windows). golang.org/x/net/webdav has no quota support, so without this
// clients assume an unlimited disk (rclone shows 1 PB). It reports whether
// it handled the request; other PROPFINDs go to the WebDAV handler untouched.
func (h *handler) quotaPropfind(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != "PROPFIND" || r.Header.Get("Depth") != "0" {
		return false
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return false
	}
	// Let the WebDAV handler re-read the body if we pass.
	r.Body = io.NopCloser(bytes.NewReader(body))
	if !bytes.Contains(body, []byte("quota-available-bytes")) && !bytes.Contains(body, []byte("quota-used-bytes")) {
		return false
	}
	if !h.isDir(r.URL.Path) {
		return false
	}
	total, free, err := diskUsage(h.localPath(r.URL.Path))
	if err != nil {
		return false
	}
	href := escapePath(path.Clean("/" + r.URL.Path))
	if !strings.HasSuffix(href, "/") {
		href += "/"
	}
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.WriteHeader(207)
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<D:multistatus xmlns:D="DAV:"><D:response><D:href>%s</D:href><D:propstat><D:prop>`+
		`<D:resourcetype><D:collection/></D:resourcetype>`+
		`<D:quota-available-bytes>%d</D:quota-available-bytes>`+
		`<D:quota-used-bytes>%d</D:quota-used-bytes>`+
		`</D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`,
		xmlEscape(href), free, total-free)
	return true
}

func xmlEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		case '"':
			b.WriteString("&quot;")
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
