package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// AddrsPath lists the server's current addresses for paired apps.
	AddrsPath     = "/__nasfone/addrs"
	conflictsPath = "/__nasfone/conflicts"
	tmpPrefix     = ".nasfone-upload-"
)

// put stores the request body at the URL path. The body goes to a temporary
// file in the same directory and is renamed over the target only once it is
// complete, so an interrupted upload never destroys or truncates an existing
// file. "If-None-Match: *" makes the upload fail with 412 instead of
// overwriting an existing file.
func (h *handler) put(w http.ResponseWriter, r *http.Request) {
	target := h.localPath(r.URL.Path)
	if path.Clean("/"+r.URL.Path) == "/" || strings.HasPrefix(filepath.Base(target), tmpPrefix) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	fi, err := os.Stat(target)
	exists := err == nil
	if exists && fi.IsDir() {
		http.Error(w, "is a directory", http.StatusMethodNotAllowed)
		return
	}
	if exists && strings.TrimSpace(r.Header.Get("If-None-Match")) == "*" {
		http.Error(w, "file already exists", http.StatusPreconditionFailed)
		return
	}
	dir := filepath.Dir(target)
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		http.Error(w, "parent folder does not exist", http.StatusConflict)
		return
	}

	tmp, err := os.CreateTemp(dir, tmpPrefix+"*.part")
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	tmpName := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			tmp.Close()
			os.Remove(tmpName)
		}
	}()
	if _, err := io.Copy(tmp, r.Body); err != nil {
		h.opt.Logf("Upload %s bị ngắt: %v (file cũ giữ nguyên)", r.URL.Path, err)
		http.Error(w, "upload interrupted", http.StatusBadRequest)
		return
	}
	tmp.Chmod(0o644) // CreateTemp makes 0600; uploads should look like normal files
	if err := tmp.Sync(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := tmp.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := os.Rename(tmpName, target); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	ok = true
	if exists {
		w.WriteHeader(http.StatusNoContent)
	} else {
		w.WriteHeader(http.StatusCreated)
	}
}

// conflicts answers POST {"base":"/dir/","paths":["a.jpg","sub/b.png"]} with
// {"conflicts":{"a.jpg":"a (1).jpg"}}: every path that already exists, mapped
// to a free "keep both" alternative that also avoids the other paths in the
// same batch.
func (h *handler) conflicts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost || !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") {
		http.Error(w, "POST application/json required", http.StatusBadRequest)
		return
	}
	var req struct {
		Base  string   `json:"base"`
		Paths []string `json:"paths"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil || len(req.Paths) > 20000 {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	base := path.Clean("/" + req.Base)
	taken := map[string]bool{} // URL paths that are or will be occupied
	for _, p := range req.Paths {
		taken[path.Join(base, p)] = true
	}
	out := map[string]string{}
	for _, p := range req.Paths {
		full := path.Join(base, p)
		if _, err := os.Stat(h.localPath(full)); err != nil {
			continue
		}
		alt, err := h.freeName(full, taken)
		if err != nil {
			continue
		}
		taken[alt] = true
		rel := strings.TrimPrefix(alt, strings.TrimSuffix(base, "/")+"/")
		out[p] = rel
	}
	writeJSON(w, http.StatusOK, map[string]any{"conflicts": out})
}

// freeName returns "dir/name (n).ext" for the smallest n not on disk and not taken.
func (h *handler) freeName(full string, taken map[string]bool) (string, error) {
	dir, file := path.Split(full)
	ext := path.Ext(file)
	stem := strings.TrimSuffix(file, ext)
	if ext == file { // dotfile like ".bashrc"
		stem, ext = file, ""
	}
	for n := 1; n < 10000; n++ {
		cand := dir + stem + " (" + strconv.Itoa(n) + ")" + ext
		if taken[cand] {
			continue
		}
		if _, err := os.Stat(h.localPath(cand)); errors.Is(err, os.ErrNotExist) {
			return cand, nil
		}
	}
	return "", errors.New("no free name")
}
