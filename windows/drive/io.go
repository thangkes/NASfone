//go:build windows

package drive

import (
	"sync"
	"time"
)

// writer sends an open file's writes straight to the server. Contiguous
// writes are joined into chunks of up to chunkSize; up to maxInFlight chunks
// travel at once. The data lives only in RAM until the server confirms it,
// and Write blocks when the pipeline is full, so a copy in File Explorer runs
// at the real upload speed and finishes only when the server has it all.
type writer struct {
	fs *FS

	mu       sync.Mutex
	path     string
	buf      []byte // pending contiguous data starting at bufOff
	bufOff   int64
	size     int64 // file size as written so far, -1 if unknown
	err      error // first failure; every later call reports it
	dirty    bool  // written since the last sync
	inflight [][2]int64
	sem      chan struct{}
	wg       sync.WaitGroup
}

func (w *writer) write(b []byte, off int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return w.err
	}
	for len(b) > 0 {
		contiguous := len(w.buf) > 0 && off == w.bufOff+int64(len(w.buf))
		if len(w.buf) > 0 && (!contiguous || len(w.buf) >= chunkSize) {
			w.sendLocked()
			if w.err != nil {
				return w.err
			}
		}
		if len(w.buf) == 0 {
			w.bufOff = off
		}
		n := min(chunkSize-len(w.buf), len(b))
		w.buf = append(w.buf, b[:n]...)
		b, off = b[n:], off+int64(n)
		if end := off; end > w.size {
			w.size = end
		}
		w.dirty = true
	}
	if len(w.buf) >= chunkSize {
		w.sendLocked()
	}
	return w.err
}

// sendLocked starts uploading the pending chunk. It is called with w.mu held
// and may release it while waiting for room in the pipeline.
func (w *writer) sendLocked() {
	data, off := w.buf, w.bufOff
	w.buf = nil
	if len(data) == 0 {
		return
	}
	end := off + int64(len(data))
	for w.overlaps(off, end) { // never let two writes of the same bytes race
		w.mu.Unlock()
		w.wg.Wait()
		w.mu.Lock()
	}
	if w.sem == nil {
		w.sem = make(chan struct{}, maxInFlight)
	}
	w.mu.Unlock()
	w.sem <- struct{}{}
	w.mu.Lock()
	r := [2]int64{off, end}
	w.inflight = append(w.inflight, r)
	w.wg.Add(1)
	p := w.path
	go func() {
		c, cancel := ctx()
		err := w.fs.R.Patch(c, p, off, data, -1, time.Time{}, false)
		cancel()
		w.mu.Lock()
		for i, x := range w.inflight {
			if x == r {
				w.inflight = append(w.inflight[:i], w.inflight[i+1:]...)
				break
			}
		}
		if err != nil && w.err == nil {
			w.err = err
		}
		w.mu.Unlock()
		<-w.sem
		w.wg.Done()
	}()
}

func (w *writer) overlaps(off, end int64) bool {
	for _, r := range w.inflight {
		if off < r[1] && r[0] < end {
			return true
		}
	}
	return false
}

// flush sends what is pending and waits for the server to confirm all of
// it; with sync it also asks the server to commit the file to storage.
func (w *writer) flush(sync bool) error {
	w.mu.Lock()
	if w.err == nil {
		w.sendLocked()
	} else {
		w.buf = nil // already failed: the copy is aborted, don't send more
	}
	w.mu.Unlock()
	w.wg.Wait()
	w.mu.Lock()
	err, dirty, p := w.err, w.dirty, w.path
	w.mu.Unlock()
	if err != nil || !sync || !dirty {
		return err
	}
	c, cancel := ctx()
	err = w.fs.R.Patch(c, p, -1, nil, -1, time.Time{}, true)
	cancel()
	w.mu.Lock()
	if err != nil && w.err == nil {
		w.err = err
	}
	if err == nil {
		w.dirty = false
	}
	w.mu.Unlock()
	return err
}

func (w *writer) currentSize() int64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.size
}

func (w *writer) setSize(n int64) {
	w.mu.Lock()
	w.size = n
	w.mu.Unlock()
}

func (w *writer) setPath(p string) {
	w.mu.Lock()
	w.path = p
	w.mu.Unlock()
}

// reader fetches byte ranges on demand and keeps a few recent segments in
// RAM. A random read (e.g. Explorer sniffing a file header) fetches a small
// window; sequential reading doubles the window up to blockSize and reads the
// next window ahead.
type reader struct {
	fs *FS

	mu      sync.Mutex
	path    string
	segs    []seg
	win     int
	lastEnd int64
	eof     int64 // known end of file, -1 if unknown
	ahead   *prefetch
}

type seg struct {
	off  int64
	data []byte
}

type prefetch struct {
	off  int64
	n    int
	done chan struct{}
	data []byte
	err  error
}

const minWindow = 256 << 10

func (r *reader) read(buf []byte, off int64) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.win == 0 {
		r.win, r.eof = minWindow, -1
	}
	if off == r.lastEnd && off > 0 {
		r.win = min(r.win*2, blockSize) // sequential: read bigger pieces
	} else if off != r.lastEnd {
		r.win = minWindow
	}
	total := 0
	for total < len(buf) {
		cur := off + int64(total)
		if r.eof >= 0 && cur >= r.eof {
			break
		}
		d, ok := r.find(cur)
		if !ok {
			want := max(r.win, len(buf)-total)
			s, err := r.fetch(cur, want)
			if err != nil {
				if total > 0 {
					break
				}
				return 0, err
			}
			d = s.data
		}
		if len(d) == 0 {
			break
		}
		total += copy(buf[total:], d)
	}
	r.lastEnd = off + int64(total)
	r.readAhead()
	return total, nil
}

// find returns cached bytes starting at off.
func (r *reader) find(off int64) ([]byte, bool) {
	for _, s := range r.segs {
		if off >= s.off && off < s.off+int64(len(s.data)) {
			return s.data[off-s.off:], true
		}
	}
	return nil, false
}

// fetch gets n bytes at off, using a matching read-ahead if one is running.
func (r *reader) fetch(off int64, n int) (seg, error) {
	var data []byte
	var err error
	if a := r.ahead; a != nil && a.off == off {
		r.ahead = nil
		r.mu.Unlock()
		<-a.done
		r.mu.Lock()
		data, err, n = a.data, a.err, a.n
	} else {
		p := r.path
		r.mu.Unlock()
		c, cancel := ctx()
		data, err = r.fs.R.Read(c, p, off, n)
		cancel()
		r.mu.Lock()
	}
	if err != nil {
		return seg{}, err
	}
	if len(data) < n {
		r.eof = off + int64(len(data))
	}
	s := seg{off, data}
	r.segs = append(r.segs, s)
	if len(r.segs) > 3 {
		r.segs = r.segs[1:]
	}
	return s, nil
}

// readAhead starts fetching the next window while the caller consumes this
// one, once reading looks sequential.
func (r *reader) readAhead() {
	if r.ahead != nil || r.win < 1<<20 {
		return
	}
	next := r.lastEnd
	for _, s := range r.segs { // start after what is already cached
		if next >= s.off && next < s.off+int64(len(s.data)) {
			next = s.off + int64(len(s.data))
		}
	}
	if r.eof >= 0 && next >= r.eof {
		return
	}
	a := &prefetch{off: next, n: r.win, done: make(chan struct{})}
	r.ahead = a
	p := r.path
	go func() {
		c, cancel := ctx()
		a.data, a.err = r.fs.R.Read(c, p, a.off, a.n)
		cancel()
		close(a.done)
	}()
}

// drop forgets cached data (the file was written through another path).
func (r *reader) drop() {
	r.mu.Lock()
	r.segs, r.ahead, r.eof = nil, nil, -1
	r.mu.Unlock()
}

func (r *reader) setPath(p string) {
	r.mu.Lock()
	r.path = p
	r.segs, r.ahead, r.eof = nil, nil, -1
	r.mu.Unlock()
}
