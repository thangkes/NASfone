package server

import (
	"errors"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Direct-write protocol, used by the Windows drive so that data written to it
// goes straight into the file on this device (no copy staged on the client).
//
//	PATCH /dir/file
//	  X-NASfone-Offset: N     write the body at byte N (creates the file if missing)
//	  X-NASfone-Size:   N     then set the file size to N (truncate or extend)
//	  X-NASfone-Mtime:  ns    then set the modification time (Unix nanoseconds)
//	  X-NASfone-Sync:   1     then flush the file to storage
//
// Any combination is allowed; a request without a body and without Offset
// only applies Size/Mtime/Sync (Size alone on a missing file creates it).
// The response carries the resulting size in X-NASfone-Size. Servers without
// this handler answer PATCH with 405, so a client never mistakes an old server
// for one that wrote its data.
const (
	hdrOffset = "X-NASfone-Offset"
	hdrSize   = "X-NASfone-Size"
	hdrMtime  = "X-NASfone-Mtime"
	hdrSync   = "X-NASfone-Sync"

	// maxPatchBody bounds one request; clients send writes in smaller chunks.
	maxPatchBody = 32 << 20
)

func (h *handler) patch(w http.ResponseWriter, r *http.Request) {
	target := h.localPath(r.URL.Path)
	if path.Clean("/"+r.URL.Path) == "/" || strings.HasPrefix(filepath.Base(target), tmpPrefix) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	num := func(name string) (int64, bool, error) {
		v := strings.TrimSpace(r.Header.Get(name))
		if v == "" {
			return 0, false, nil
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, true, errors.New("bad " + name)
		}
		return n, true, nil
	}
	off, hasOff, err1 := num(hdrOffset)
	size, hasSize, err2 := num(hdrSize)
	mtime, hasMtime, err3 := num(hdrMtime)
	if err := errors.Join(err1, err2, err3); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		http.Error(w, "is a directory", http.StatusMethodNotAllowed)
		return
	}
	if st, err := os.Stat(filepath.Dir(target)); err != nil || !st.IsDir() {
		http.Error(w, "parent folder does not exist", http.StatusConflict)
		return
	}

	flags := os.O_WRONLY
	if hasOff || hasSize {
		flags |= os.O_CREATE
	}
	f, err := os.OpenFile(target, flags, 0o644)
	if errors.Is(err, os.ErrNotExist) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	defer f.Close()

	if hasOff {
		body := http.MaxBytesReader(w, r.Body, maxPatchBody)
		if _, err := io.Copy(io.NewOffsetWriter(f, off), body); err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
				return
			}
			h.opt.Logf("Ghi %s tại %d bị ngắt: %v", r.URL.Path, off, err)
			http.Error(w, "write interrupted", http.StatusBadRequest)
			return
		}
	}
	if hasSize {
		if err := f.Truncate(size); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if r.Header.Get(hdrSync) == "1" {
		if err := f.Sync(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}
	if err := f.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if hasMtime {
		t := time.Unix(0, mtime)
		os.Chtimes(target, t, t)
	}
	if fi, err := os.Stat(target); err == nil {
		w.Header().Set(hdrSize, strconv.FormatInt(fi.Size(), 10))
	}
	w.WriteHeader(http.StatusNoContent)
}
