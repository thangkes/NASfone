//go:build windows

package drive

import (
	"context"
	"errors"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/winfsp/cgofuse/fuse"
)

const (
	chunkSize   = 4 << 20 // bytes per PATCH; the server accepts up to 32 MB
	maxInFlight = 4       // PATCHes in flight per open file (hides network latency)
	blockSize   = 4 << 20 // read granularity; the next block is fetched ahead
	listTTL     = 10 * time.Second
	opTimeout   = 2 * time.Minute
)

// FS implements fuse.FileSystemInterface over a Remote.
type FS struct {
	fuse.FileSystemBase
	R        *Remote
	ReadOnly bool                         // user role: no writes at all
	OnError  func(path string, err error) // a write that failed after close
	OnMount  func()                       // called once the drive is mounted

	mu      sync.Mutex
	dirs    map[string]*dirCache // folder path (lower case) -> listing
	handles map[uint64]*handle
	nextFH  uint64

	quotaAt               time.Time
	quotaAvail, quotaUsed int64
}

type dirCache struct {
	at      time.Time
	entries map[string]Entry // lower-case name -> entry (Windows names ignore case)
}

func New(r *Remote, readOnly bool) *FS {
	return &FS{R: r, ReadOnly: readOnly, dirs: map[string]*dirCache{}, handles: map[uint64]*handle{}}
}

func ctx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), opTimeout)
}

func errno(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, ErrNotFound):
		return -fuse.ENOENT
	case errors.Is(err, ErrExists):
		return -fuse.EEXIST
	case errors.Is(err, ErrDenied):
		return -fuse.EACCES
	case errors.Is(err, ErrNoSpace):
		return -fuse.ENOSPC
	}
	return -fuse.EIO
}

// mountTime stands in for missing modification times.
var mountTime = time.Now()

func key(p string) string { return strings.ToLower(path.Clean("/" + p)) }

// ---- metadata ----

// listing returns a folder's cached listing, fetching it when stale.
func (fs *FS) listing(dir string) (*dirCache, error) {
	k := key(dir)
	fs.mu.Lock()
	dc := fs.dirs[k]
	fs.mu.Unlock()
	if dc != nil && time.Since(dc.at) < listTTL {
		return dc, nil
	}
	c, cancel := ctx()
	defer cancel()
	list, err := fs.R.List(c, fs.real(dir))
	if err != nil {
		return nil, err
	}
	dc = &dirCache{at: time.Now(), entries: map[string]Entry{}}
	for _, e := range list {
		dc.entries[strings.ToLower(e.Name)] = e
	}
	fs.mu.Lock()
	fs.dirs[k] = dc
	fs.mu.Unlock()
	return dc, nil
}

// real maps a path in any letter case to the server's actual names, using
// the cached listings (Windows ignores case, Android storage does not).
func (fs *FS) real(p string) string {
	p = path.Clean("/" + p)
	if p == "/" {
		return p
	}
	parts := strings.Split(p[1:], "/")
	out := ""
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, part := range parts {
		if dc := fs.dirs[key(out+"/")]; dc != nil {
			if e, ok := dc.entries[strings.ToLower(part)]; ok {
				part = e.Name
			}
		}
		out += "/" + part
	}
	return out
}

// lookup finds one entry through its parent's listing.
func (fs *FS) lookup(p string) (Entry, error) {
	p = path.Clean("/" + p)
	if p == "/" {
		return Entry{Dir: true}, nil
	}
	dc, err := fs.listing(path.Dir(p))
	if err != nil {
		return Entry{}, err
	}
	if e, ok := dc.entries[strings.ToLower(path.Base(p))]; ok {
		return e, nil
	}
	return Entry{}, ErrNotFound
}

// remember updates the cached parent listing after a local change.
func (fs *FS) remember(p string, e *Entry) {
	p = path.Clean("/" + p)
	fs.mu.Lock()
	defer fs.mu.Unlock()
	dc := fs.dirs[key(path.Dir(p))]
	if dc == nil {
		return
	}
	name := strings.ToLower(path.Base(p))
	if e == nil {
		delete(dc.entries, name)
	} else {
		dc.entries[name] = *e
	}
}

func (fs *FS) forgetDir(p string) {
	fs.mu.Lock()
	k := key(p)
	for d := range fs.dirs {
		if d == k || strings.HasPrefix(d, k+"/") {
			delete(fs.dirs, d)
		}
	}
	fs.mu.Unlock()
}

func (fs *FS) fill(st *fuse.Stat_t, e Entry) {
	*st = fuse.Stat_t{Nlink: 1, Uid: ^uint32(0), Gid: ^uint32(0)}
	if e.Dir {
		st.Mode = fuse.S_IFDIR | 0o777
	} else {
		st.Mode = fuse.S_IFREG | 0o666
		st.Size = e.Size
	}
	if fs.ReadOnly {
		st.Mode &^= 0o222
	}
	mt := e.MTime
	if mt.IsZero() {
		mt = mountTime // the root and entries without a date: Windows rejects year 1
	}
	ts := fuse.NewTimespec(mt)
	st.Mtim, st.Ctim, st.Atim, st.Birthtim = ts, ts, ts, ts
}

// ---- file handles ----

type handle struct {
	path string // real path on the server
	w    *writer
	r    *reader
}

func (fs *FS) newHandle(p string, write bool, size int64) uint64 {
	h := &handle{path: p}
	if write {
		h.w = &writer{fs: fs, path: p, size: size}
	}
	h.r = &reader{fs: fs, path: p}
	fs.mu.Lock()
	fs.nextFH++
	fh := fs.nextFH
	fs.handles[fh] = h
	fs.mu.Unlock()
	return fh
}

func (fs *FS) handle(fh uint64) *handle {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.handles[fh]
}

// flushPath waits for every open writer of p, so reads, renames and size
// changes see all data written so far.
func (fs *FS) flushPath(p string) error {
	fs.mu.Lock()
	var ws []*writer
	for _, h := range fs.handles {
		if h.w != nil && strings.EqualFold(h.path, p) {
			ws = append(ws, h.w)
		}
	}
	fs.mu.Unlock()
	var first error
	for _, w := range ws {
		if err := w.flush(false); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// ---- fuse operations ----

func (fs *FS) Statfs(p string, st *fuse.Statfs_t) int {
	fs.mu.Lock()
	stale := time.Since(fs.quotaAt) > 30*time.Second
	fs.mu.Unlock()
	if stale {
		c, cancel := ctx()
		avail, used, err := fs.R.Quota(c)
		cancel()
		if err == nil {
			fs.mu.Lock()
			fs.quotaAt, fs.quotaAvail, fs.quotaUsed = time.Now(), avail, used
			fs.mu.Unlock()
		}
	}
	fs.mu.Lock()
	avail, used := fs.quotaAvail, fs.quotaUsed
	fs.mu.Unlock()
	const bs = 4096
	*st = fuse.Statfs_t{Bsize: bs, Frsize: bs, Namemax: 255,
		Blocks: uint64(avail+used) / bs, Bfree: uint64(avail) / bs, Bavail: uint64(avail) / bs}
	return 0
}

func (fs *FS) Getattr(p string, st *fuse.Stat_t, fh uint64) int {
	if h := fs.handle(fh); h != nil && h.w != nil {
		if e, err := fs.lookup(p); err == nil {
			if sz := h.w.currentSize(); sz >= 0 {
				e.Size = sz
			}
			fs.fill(st, e)
			return 0
		}
	}
	e, err := fs.lookup(p)
	if err != nil {
		return errno(err)
	}
	fs.fill(st, e)
	return 0
}

func (fs *FS) Opendir(p string) (int, uint64) {
	e, err := fs.lookup(p)
	if err != nil {
		return errno(err), ^uint64(0)
	}
	if !e.Dir {
		return -fuse.ENOTDIR, ^uint64(0)
	}
	return 0, 0
}

func (fs *FS) Readdir(p string, fill func(string, *fuse.Stat_t, int64) bool, ofst int64, fh uint64) int {
	dc, err := fs.listing(p)
	if err != nil {
		return errno(err)
	}
	var st fuse.Stat_t
	fs.fill(&st, Entry{Dir: true})
	fill(".", &st, 0)
	fill("..", nil, 0)
	fs.mu.Lock()
	list := make([]Entry, 0, len(dc.entries))
	for _, e := range dc.entries {
		list = append(list, e)
	}
	fs.mu.Unlock()
	for _, e := range list {
		var s fuse.Stat_t
		fs.fill(&s, e)
		if !fill(e.Name, &s, 0) {
			break
		}
	}
	return 0
}

func (fs *FS) Open(p string, flags int) (int, uint64) {
	e, err := fs.lookup(p)
	if err != nil {
		return errno(err), ^uint64(0)
	}
	if e.Dir {
		return -fuse.EISDIR, ^uint64(0)
	}
	write := flags&fuse.O_ACCMODE != fuse.O_RDONLY
	if write && fs.ReadOnly {
		return -fuse.EROFS, ^uint64(0)
	}
	real := fs.real(p)
	if write && flags&fuse.O_TRUNC != 0 {
		if rc := fs.Truncate(p, 0, ^uint64(0)); rc != 0 {
			return rc, ^uint64(0)
		}
	}
	size := e.Size
	if write && flags&fuse.O_TRUNC != 0 {
		size = 0
	}
	return 0, fs.newHandle(real, write, size)
}

func (fs *FS) Create(p string, flags int, mode uint32) (int, uint64) {
	if fs.ReadOnly {
		return -fuse.EROFS, ^uint64(0)
	}
	if _, err := fs.lookup(path.Dir(p)); err != nil {
		return errno(err), ^uint64(0)
	}
	real := fs.real(p)
	c, cancel := ctx()
	err := fs.R.Patch(c, real, -1, nil, 0, time.Time{}, false) // create empty (or truncate)
	cancel()
	if err != nil {
		return errno(err), ^uint64(0)
	}
	fs.remember(real, &Entry{Name: path.Base(real), MTime: time.Now()})
	return 0, fs.newHandle(real, true, 0)
}

func (fs *FS) Truncate(p string, size int64, fh uint64) int {
	if fs.ReadOnly {
		return -fuse.EROFS
	}
	real := fs.real(p)
	if err := fs.flushPath(real); err != nil {
		return errno(err)
	}
	c, cancel := ctx()
	err := fs.R.Patch(c, real, -1, nil, size, time.Time{}, false)
	cancel()
	if err != nil {
		return errno(err)
	}
	if h := fs.handle(fh); h != nil && h.w != nil {
		h.w.setSize(size)
	}
	if e, err := fs.lookup(real); err == nil {
		e.Size, e.MTime = size, time.Now()
		fs.remember(real, &e)
	}
	return 0
}

func (fs *FS) Read(p string, buf []byte, off int64, fh uint64) int {
	h := fs.handle(fh)
	if h == nil {
		return -fuse.EBADF
	}
	if err := fs.flushPath(h.path); err != nil {
		return errno(err)
	}
	n, err := h.r.read(buf, off)
	if err != nil {
		return errno(err)
	}
	return n
}

func (fs *FS) Write(p string, buf []byte, off int64, fh uint64) int {
	h := fs.handle(fh)
	if h == nil || h.w == nil {
		return -fuse.EBADF
	}
	if err := h.w.write(buf, off); err != nil {
		return errno(err)
	}
	h.r.drop()
	return len(buf)
}

// Flush, Fsync and Utimens wait until the server holds everything written,
// so CopyFile (which sets the file times last) only succeeds once the data is
// really on the server.
func (fs *FS) Flush(p string, fh uint64) int {
	if h := fs.handle(fh); h != nil && h.w != nil {
		return errno(h.w.flush(true))
	}
	return 0
}

func (fs *FS) Fsync(p string, datasync bool, fh uint64) int { return fs.Flush(p, fh) }

func (fs *FS) Release(p string, fh uint64) int {
	h := fs.handle(fh)
	if h == nil {
		return 0
	}
	if h.w != nil {
		if err := h.w.flush(true); err != nil && fs.OnError != nil {
			fs.OnError(h.path, err) // Windows ignores close errors: tell the user
		}
		if sz := h.w.currentSize(); sz >= 0 {
			if e, err := fs.lookup(h.path); err == nil {
				e.Size, e.MTime = sz, time.Now()
				fs.remember(h.path, &e)
			}
		}
	}
	fs.mu.Lock()
	delete(fs.handles, fh)
	fs.mu.Unlock()
	return 0
}

func (fs *FS) Utimens(p string, tmsp []fuse.Timespec) int {
	if fs.ReadOnly {
		return -fuse.EROFS
	}
	real := fs.real(p)
	if err := fs.flushPath(real); err != nil {
		return errno(err)
	}
	e, err := fs.lookup(real)
	if err != nil {
		return errno(err)
	}
	if e.Dir || len(tmsp) < 2 {
		return 0 // folder times are not kept
	}
	mt := tmsp[1].Time()
	c, cancel := ctx()
	err = fs.R.Patch(c, real, -1, nil, -1, mt, true)
	cancel()
	if err != nil {
		return errno(err)
	}
	e.MTime = mt
	fs.remember(real, &e)
	return 0
}

func (fs *FS) Mkdir(p string, mode uint32) int {
	if fs.ReadOnly {
		return -fuse.EROFS
	}
	real := fs.real(p)
	c, cancel := ctx()
	err := fs.R.Mkdir(c, real)
	cancel()
	if err != nil {
		return errno(err)
	}
	fs.remember(real, &Entry{Name: path.Base(real), Dir: true, MTime: time.Now()})
	return 0
}

func (fs *FS) Unlink(p string) int {
	if fs.ReadOnly {
		return -fuse.EROFS
	}
	real := fs.real(p)
	c, cancel := ctx()
	err := fs.R.Delete(c, real)
	cancel()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return errno(err)
	}
	fs.remember(real, nil)
	return 0
}

// Rmdir only removes empty folders: WebDAV DELETE would remove a folder with
// everything in it.
func (fs *FS) Rmdir(p string) int {
	if fs.ReadOnly {
		return -fuse.EROFS
	}
	real := fs.real(p)
	c, cancel := ctx()
	list, err := fs.R.List(c, real)
	cancel()
	if err != nil {
		return errno(err)
	}
	if len(list) > 0 {
		return -fuse.ENOTEMPTY
	}
	c, cancel = ctx()
	err = fs.R.Delete(c, real)
	cancel()
	if err != nil && !errors.Is(err, ErrNotFound) {
		return errno(err)
	}
	fs.remember(real, nil)
	fs.forgetDir(real)
	return 0
}

func (fs *FS) Rename(oldp, newp string) int {
	if fs.ReadOnly {
		return -fuse.EROFS
	}
	from, to := fs.real(oldp), fs.real(newp)
	if err := fs.flushPath(from); err != nil {
		return errno(err)
	}
	e, err := fs.lookup(from)
	if err != nil {
		return errno(err)
	}
	c, cancel := ctx()
	err = fs.R.Move(c, from, to, true)
	cancel()
	if err != nil {
		return errno(err)
	}
	fs.remember(from, nil)
	e.Name = path.Base(to)
	fs.remember(to, &e)
	if e.Dir {
		fs.forgetDir(from)
	}
	fs.mu.Lock()
	for _, h := range fs.handles {
		if strings.EqualFold(h.path, from) {
			h.path = to
			if h.w != nil {
				h.w.setPath(to)
			}
			h.r.setPath(to)
		}
	}
	fs.mu.Unlock()
	return 0
}

func (fs *FS) Chmod(p string, mode uint32) int                 { return 0 }
func (fs *FS) Chown(p string, uid uint32, gid uint32) int      { return 0 }
func (fs *FS) Access(p string, mask uint32) int                { return 0 }
func (fs *FS) Releasedir(p string, fh uint64) int              { return 0 }
func (fs *FS) Fsyncdir(p string, datasync bool, fh uint64) int { return 0 }

// Init runs once WinFsp has mounted the drive.
func (fs *FS) Init() {
	if fs.OnMount != nil {
		fs.OnMount()
	}
}

// Writing counts files open for writing that still have data on its way
// to the server (quitting now would cut those copies short).
func (fs *FS) Writing() int {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	n := 0
	for _, h := range fs.handles {
		if h.w != nil {
			n++
		}
	}
	return n
}
