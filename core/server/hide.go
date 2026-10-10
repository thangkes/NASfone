package server

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/webdav"
)

// Hidden folders are kept out of the share even when the shared folder
// contains them: on Windows, sharing C:\Users\<name> would otherwise expose
// %APPDATA%\NASfone-Server (the server's private key, paired apps, browser
// sessions). A hidden folder and everything in it answers 404 to every
// method, for every role, and is left out of listings. The rest of the share
// keeps working.
//
// Folders are matched by file identity (os.SameFile), not by name, so every
// other spelling of the same folder is hidden too: letter case, 8.3 short
// names, trailing dots, junctions and symlinks.

// hiddenRefresh is how long the resolved hidden folders are cached; folders
// that do not exist yet (no server set up in that profile) are picked up
// once they appear.
const hiddenRefresh = 30 * time.Second

type hiddenDir struct {
	fi     os.FileInfo // the folder itself
	parent os.FileInfo // its parent folder, to filter listings by name
	name   string
}

type hider struct {
	list func() []string // absolute folder paths

	mu   sync.Mutex
	at   time.Time
	dirs []hiddenDir
}

func newHider(list func() []string) *hider {
	if list == nil {
		return nil
	}
	return &hider{list: list}
}

// load returns the hidden folders that exist right now.
func (x *hider) load() []hiddenDir {
	if x == nil {
		return nil
	}
	x.mu.Lock()
	defer x.mu.Unlock()
	if !x.at.IsZero() && time.Since(x.at) < hiddenRefresh {
		return x.dirs
	}
	var dirs []hiddenDir
	for _, p := range x.list() {
		if p == "" || !filepath.IsAbs(p) {
			continue
		}
		p = filepath.Clean(p)
		fi, err := os.Stat(p)
		if err != nil || !fi.IsDir() {
			continue
		}
		pfi, err := os.Stat(filepath.Dir(p))
		if err != nil {
			continue
		}
		dirs = append(dirs, hiddenDir{fi: fi, parent: pfi, name: filepath.Base(p)})
	}
	x.dirs, x.at = dirs, time.Now()
	return dirs
}

// hides reports whether local (a path under root) is a hidden folder or lies
// inside one. Every existing folder from local up to root is compared, so a
// path that reaches a hidden folder through a junction is caught as well.
func (x *hider) hides(root, local string) bool {
	dirs := x.load()
	if len(dirs) == 0 {
		return false
	}
	root = filepath.Clean(root)
	for p := filepath.Clean(local); ; {
		if fi, err := os.Stat(p); err == nil {
			for _, d := range dirs {
				if os.SameFile(fi, d.fi) {
					return true
				}
			}
		}
		parent := filepath.Dir(p)
		if p == root || parent == p || len(parent) < len(root) {
			return false
		}
		p = parent
	}
}

// entryFilter returns a predicate that reports whether an entry of folder dir
// must be left out of a listing, or nil when nothing in dir is hidden.
func (x *hider) entryFilter(dir string) func(name string, mode fs.FileMode) bool {
	dirs := x.load()
	if len(dirs) == 0 {
		return nil
	}
	dfi, err := os.Stat(dir)
	if err != nil {
		return nil
	}
	var here []hiddenDir // hidden folders whose parent is dir
	for _, d := range dirs {
		if os.SameFile(dfi, d.parent) {
			here = append(here, d)
		}
	}
	return func(name string, mode fs.FileMode) bool {
		check := mode&(fs.ModeSymlink|fs.ModeIrregular) != 0 // junctions and links may point anywhere
		for _, d := range here {
			if strings.EqualFold(name, d.name) {
				check = true
			}
		}
		if !check {
			return false
		}
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			return false
		}
		for _, d := range dirs {
			if os.SameFile(fi, d.fi) {
				return true
			}
		}
		return false
	}
}

// hidden reports whether the URL path is (inside) a hidden folder.
func (h *handler) hidden(urlPath string) bool {
	return h.hide.hides(h.opt.Root, h.localPath(urlPath))
}

// hidingFS is the WebDAV file system with the hidden folders taken out.
// Destinations of COPY and MOVE go through it too.
type hidingFS struct {
	webdav.Dir
	h *handler
}

func (f hidingFS) Mkdir(ctx context.Context, name string, perm os.FileMode) error {
	if f.h.hidden(name) {
		return os.ErrNotExist
	}
	return f.Dir.Mkdir(ctx, name, perm)
}

func (f hidingFS) OpenFile(ctx context.Context, name string, flag int, perm os.FileMode) (webdav.File, error) {
	if f.h.hidden(name) {
		return nil, os.ErrNotExist
	}
	file, err := f.Dir.OpenFile(ctx, name, flag, perm)
	if err != nil {
		return nil, err
	}
	return hidingFile{File: file, dir: f.h.localPath(name), h: f.h}, nil
}

func (f hidingFS) RemoveAll(ctx context.Context, name string) error {
	if f.h.hidden(name) {
		return os.ErrNotExist
	}
	return f.Dir.RemoveAll(ctx, name)
}

func (f hidingFS) Rename(ctx context.Context, oldName, newName string) error {
	if f.h.hidden(oldName) || f.h.hidden(newName) {
		return os.ErrNotExist
	}
	return f.Dir.Rename(ctx, oldName, newName)
}

func (f hidingFS) Stat(ctx context.Context, name string) (os.FileInfo, error) {
	if f.h.hidden(name) {
		return nil, os.ErrNotExist
	}
	return f.Dir.Stat(ctx, name)
}

// hidingFile leaves hidden folders out of PROPFIND listings.
type hidingFile struct {
	webdav.File
	dir string
	h   *handler
}

func (f hidingFile) Readdir(count int) ([]os.FileInfo, error) {
	list, err := f.File.Readdir(count)
	if skip := f.h.hide.entryFilter(f.dir); skip != nil {
		kept := list[:0]
		for _, fi := range list {
			if !skip(fi.Name(), fi.Mode()) {
				kept = append(kept, fi)
			}
		}
		list = kept
	}
	return list, err
}
