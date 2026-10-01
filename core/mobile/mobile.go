// Package mobile is the gomobile-facing API used by the Android apps.
// Keep the exported surface to strings, bools, ints and errors so gobind can
// translate it.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"pocketnas/core/auth"
	"pocketnas/core/server"

	"tailscale.com/envknob"
	"tailscale.com/ipn"
	"tailscale.com/net/netmon"
	"tailscale.com/tsnet"
)

// Host is implemented on the Kotlin side.
type Host interface {
	OnStatus(statusJSON string)
	OnLog(line string)
	// OnEvent reports security events: kind is "login", "revoke" or "code_spent".
	OnEvent(kind, detail string)
	// Interfaces returns one line per network interface:
	// name|index|mtu|flags|addr/prefix,addr/prefix
	// where flags is a subset of "ulpmb" (up, loopback, point-to-point, multicast, broadcast).
	Interfaces() string
}

// Config is passed to Start as JSON.
type Config struct {
	StateDir   string `json:"stateDir"`
	AuthFile   string `json:"authFile"` // signed-in browser devices; kept apart from the tailnet state
	RootDir    string `json:"rootDir"`
	Hostname   string `json:"hostname"`
	ControlURL string `json:"controlURL"` // empty = Tailscale's default; set for Headscale
	LanPort    int    `json:"lanPort"`
	Password   string `json:"password"`
	Funnel     bool   `json:"funnel"`
	Verbose    bool   `json:"verbose"`
}

// Status is reported to the host as JSON.
type Status struct {
	Running      bool     `json:"running"`
	BackendState string   `json:"backendState"`
	AuthURL      string   `json:"authURL,omitempty"`
	LoginName    string   `json:"loginName,omitempty"`
	TailnetName  string   `json:"tailnetName,omitempty"`
	DNSName      string   `json:"dnsName,omitempty"`
	TailscaleIPs []string `json:"tailscaleIPs,omitempty"`
	LanPort      int      `json:"lanPort"`
	FunnelWanted bool     `json:"funnelWanted"`
	FunnelURL    string   `json:"funnelURL,omitempty"`
	FunnelError  string   `json:"funnelError,omitempty"`
	Error        string   `json:"error,omitempty"`
}

type node struct {
	cfg  Config
	host Host

	ctx    context.Context
	cancel context.CancelFunc

	ts      *tsnet.Server
	auth    *auth.Store
	handler http.Handler
	servers []*http.Server

	mu             sync.Mutex
	status         Status
	lastJSON       string
	loginRequested bool
	funnelSrv      *http.Server
}

var (
	mu      sync.Mutex
	current *node
	hostRef Host // used by the netmon interface getter
	once    sync.Once
)

// SetCrashFile makes Go write panic traces to path (logcat is unreadable on some ROMs).
func SetCrashFile(path string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	return debug.SetCrashOutput(f, debug.CrashOptions{})
}

// Start launches the LAN listener and the embedded Tailscale node.
func Start(configJSON string, host Host) error {
	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.StateDir == "" || cfg.RootDir == "" {
		return errors.New("config: stateDir and rootDir are required")
	}
	if cfg.Password == "" {
		return errors.New("config: password is required")
	}
	if cfg.Hostname == "" {
		cfg.Hostname = "pocketnas"
	}
	if cfg.LanPort == 0 {
		cfg.LanPort = 8080
	}

	mu.Lock()
	defer mu.Unlock()
	if current != nil {
		return errors.New("already running")
	}

	hostRef = host
	once.Do(func() {
		// Android (API 30+) blocks net.Interfaces for apps; ask Kotlin instead.
		netmon.RegisterInterfaceGetter(getInterfaces)
		// Never upload logs to Tailscale's log service.
		envknob.SetNoLogsNoSupport()
	})

	if err := os.MkdirAll(cfg.StateDir, 0o700); err != nil {
		return err
	}
	// Android apps get no HOME/XDG/TMPDIR; tailscale's logpolicy panics without a
	// writable cache or temp dir, so point them inside the app's private storage.
	for env, sub := range map[string]string{"HOME": "home", "XDG_CACHE_HOME": "cache", "TMPDIR": "tmp"} {
		if os.Getenv(env) == "" {
			d := filepath.Join(cfg.StateDir, sub)
			os.MkdirAll(d, 0o700)
			os.Setenv(env, d)
		}
	}
	if err := os.MkdirAll(cfg.RootDir, 0o755); err != nil {
		return fmt.Errorf("không tạo được thư mục lưu trữ: %w", err)
	}

	if cfg.AuthFile == "" {
		cfg.AuthFile = filepath.Join(filepath.Dir(cfg.StateDir), "auth.json")
	}
	store, err := auth.Open(cfg.AuthFile)
	if err != nil {
		return fmt.Errorf("auth store: %w", err)
	}
	store.OnEvent = host.OnEvent

	ctx, cancel := context.WithCancel(context.Background())
	n := &node{cfg: cfg, host: host, ctx: ctx, cancel: cancel, auth: store}
	n.status = Status{LanPort: cfg.LanPort, FunnelWanted: cfg.Funnel, BackendState: "Starting"}
	n.handler = server.NewHandler(server.Options{Root: cfg.RootDir, Password: cfg.Password, Auth: store, Logf: n.logf})

	// LAN / hotspot listener.
	lanLn, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.LanPort))
	if err != nil {
		cancel()
		return fmt.Errorf("không mở được cổng %d: %w", cfg.LanPort, err)
	}
	n.serve(lanLn, "LAN")

	// Embedded Tailscale node.
	n.ts = &tsnet.Server{
		Dir:        cfg.StateDir,
		Hostname:   cfg.Hostname,
		ControlURL: cfg.ControlURL,
		UserLogf:   n.logf,
	}
	if cfg.Verbose {
		n.ts.Logf = func(format string, args ...any) { n.host.OnLog("[ts] " + fmt.Sprintf(format, args...)) }
	} else {
		n.ts.Logf = func(string, ...any) {}
	}
	if err := n.ts.Start(); err != nil {
		n.shutdown()
		return fmt.Errorf("tailscale: %w", err)
	}
	tsLn, err := n.ts.Listen("tcp", ":80")
	if err != nil {
		n.shutdown()
		return fmt.Errorf("tailscale listen: %w", err)
	}
	n.serve(tsLn, "Tailnet")

	current = n
	go n.loop()
	n.logf("Đã khởi động: LAN cổng %d, tên máy tailnet %q", cfg.LanPort, cfg.Hostname)
	return nil
}

// Stop shuts everything down. Safe to call when not running.
func Stop() {
	mu.Lock()
	n := current
	current = nil
	mu.Unlock()
	if n != nil {
		n.shutdown()
		n.logf("Đã dừng")
	}
}

// Login asks the control server for a fresh login URL (reported via OnStatus).
func Login() error {
	n := get()
	if n == nil {
		return errors.New("not running")
	}
	lc, err := n.ts.LocalClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, 20*time.Second)
	defer cancel()
	n.mu.Lock()
	n.loginRequested = true
	n.mu.Unlock()
	return lc.StartLoginInteractive(ctx)
}

// Logout signs this node out of its tailnet so another account can be used.
func Logout() error {
	n := get()
	if n == nil {
		return errors.New("not running")
	}
	n.closeFunnel()
	lc, err := n.ts.LocalClient()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(n.ctx, 20*time.Second)
	defer cancel()
	if err := lc.Logout(ctx); err != nil {
		return err
	}
	n.mu.Lock()
	n.loginRequested = false
	n.mu.Unlock()
	n.logf("Đã đăng xuất khỏi tailnet")
	return nil
}

// SetFunnel turns the public Funnel listener on or off.
func SetFunnel(enabled bool) {
	n := get()
	if n == nil {
		return
	}
	n.mu.Lock()
	n.cfg.Funnel = enabled
	n.status.FunnelWanted = enabled
	n.status.FunnelError = ""
	n.mu.Unlock()
	if !enabled {
		n.closeFunnel()
	}
	n.refresh()
}

// NewLoginCode issues a fresh one-time login code: {"code":"ABCD-2345","expires":<unix ms>}.
func NewLoginCode() (string, error) {
	n := get()
	if n == nil {
		return "", errors.New("server chưa chạy")
	}
	code, exp := n.auth.NewCode()
	n.logf("Đã tạo mã đăng nhập mới")
	return codeJSON(code, exp), nil
}

// LoginCode returns the active code JSON, or "" when none is active.
func LoginCode() string {
	n := get()
	if n == nil {
		return ""
	}
	code, exp, ok := n.auth.CurrentCode()
	if !ok {
		return ""
	}
	return codeJSON(code, exp)
}

func codeJSON(code string, exp time.Time) string {
	b, _ := json.Marshal(map[string]any{"code": code, "expires": exp.UnixMilli()})
	return string(b)
}

// Devices returns the signed-in browser devices as a JSON array.
func Devices() string {
	n := get()
	if n == nil {
		return "[]"
	}
	b, _ := json.Marshal(n.auth.List())
	return string(b)
}

// RevokeDevice signs one device out.
func RevokeDevice(id string) bool {
	n := get()
	return n != nil && n.auth.Revoke(id)
}

// RevokeAllDevices signs every browser device out.
func RevokeAllDevices() {
	if n := get(); n != nil {
		n.auth.RevokeAll()
	}
}

// CurrentStatus returns the latest status JSON ("" when stopped).
func CurrentStatus() string {
	n := get()
	if n == nil {
		return ""
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.lastJSON
}

func get() *node {
	mu.Lock()
	defer mu.Unlock()
	return current
}

func (n *node) logf(format string, args ...any) {
	n.host.OnLog(fmt.Sprintf(format, args...))
}

func (n *node) serve(ln net.Listener, name string) *http.Server {
	srv := &http.Server{Handler: server.WithVia(n.handler, name), ReadHeaderTimeout: 30 * time.Second}
	n.mu.Lock()
	n.servers = append(n.servers, srv)
	n.mu.Unlock()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			n.logf("%s server: %v", name, err)
		}
	}()
	return srv
}

func (n *node) shutdown() {
	n.cancel()
	n.closeFunnel()
	n.mu.Lock()
	servers := n.servers
	n.servers = nil
	n.mu.Unlock()
	for _, s := range servers {
		s.Close()
	}
	if n.ts != nil {
		n.ts.Close()
	}
}

func (n *node) closeFunnel() {
	n.mu.Lock()
	s := n.funnelSrv
	n.funnelSrv = nil
	n.status.FunnelURL = ""
	n.mu.Unlock()
	if s != nil {
		s.Close()
	}
}

// loop polls the backend state, triggers interactive login, and manages Funnel.
func (n *node) loop() {
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		n.refresh()
		select {
		case <-n.ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (n *node) refresh() {
	if n.ctx.Err() != nil {
		return
	}
	lc, err := n.ts.LocalClient()
	if err != nil {
		n.setError(err)
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 5*time.Second)
	defer cancel()
	st, err := lc.StatusWithoutPeers(ctx)
	if err != nil {
		n.setError(err)
		return
	}

	n.mu.Lock()
	s := &n.status
	s.Running = true
	s.Error = ""
	s.BackendState = st.BackendState
	s.AuthURL = st.AuthURL
	s.TailscaleIPs = s.TailscaleIPs[:0]
	for _, ip := range st.TailscaleIPs {
		s.TailscaleIPs = append(s.TailscaleIPs, ip.String())
	}
	s.DNSName, s.LoginName, s.TailnetName = "", "", ""
	if st.Self != nil {
		s.DNSName = strings.TrimSuffix(st.Self.DNSName, ".")
		if u, ok := st.User[st.Self.UserID]; ok {
			s.LoginName = u.LoginName
		}
	}
	if st.CurrentTailnet != nil {
		s.TailnetName = st.CurrentTailnet.Name
	}
	needLogin := st.BackendState == ipn.NeedsLogin.String() && st.AuthURL == "" && !n.loginRequested
	if needLogin {
		n.loginRequested = true
	}
	wantFunnel := n.cfg.Funnel && st.BackendState == ipn.Running.String() && n.funnelSrv == nil && n.status.FunnelError == ""
	n.mu.Unlock()

	if needLogin {
		go func() {
			ctx, cancel := context.WithTimeout(n.ctx, 20*time.Second)
			defer cancel()
			if err := lc.StartLoginInteractive(ctx); err != nil {
				n.logf("StartLoginInteractive: %v", err)
			}
		}()
	}
	if wantFunnel {
		n.startFunnel()
	}
	n.publish()
}

func (n *node) startFunnel() {
	ln, err := n.ts.ListenFunnel("tcp", ":443")
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		n.status.FunnelError = err.Error()
		n.logf("Funnel lỗi: %v", err)
		return
	}
	srv := &http.Server{Handler: server.WithVia(n.handler, "Funnel"), ReadHeaderTimeout: 30 * time.Second}
	n.funnelSrv = srv
	n.status.FunnelURL = "https://" + n.status.DNSName
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			n.logf("Funnel server: %v", err)
		}
	}()
	n.logf("Funnel đang mở: %s", n.status.FunnelURL)
}

func (n *node) setError(err error) {
	n.mu.Lock()
	n.status.Error = err.Error()
	n.mu.Unlock()
	n.publish()
}

func (n *node) publish() {
	n.mu.Lock()
	b, _ := json.Marshal(n.status)
	js := string(b)
	changed := js != n.lastJSON
	n.lastJSON = js
	n.mu.Unlock()
	if changed {
		n.host.OnStatus(js)
	}
}

func getInterfaces() ([]netmon.Interface, error) {
	h := hostRef
	if h == nil {
		return nil, errors.New("no host")
	}
	var out []netmon.Interface
	for _, line := range strings.Split(h.Interfaces(), "\n") {
		parts := strings.Split(strings.TrimSpace(line), "|")
		if len(parts) != 5 {
			continue
		}
		idx, _ := strconv.Atoi(parts[1])
		mtu, _ := strconv.Atoi(parts[2])
		var flags net.Flags
		for _, c := range parts[3] {
			switch c {
			case 'u':
				flags |= net.FlagUp | net.FlagRunning
			case 'l':
				flags |= net.FlagLoopback
			case 'p':
				flags |= net.FlagPointToPoint
			case 'm':
				flags |= net.FlagMulticast
			case 'b':
				flags |= net.FlagBroadcast
			}
		}
		ifc := netmon.Interface{Interface: &net.Interface{Index: idx, MTU: mtu, Name: parts[0], Flags: flags}}
		ifc.AltAddrs = []net.Addr{}
		for _, a := range strings.Split(parts[4], ",") {
			if a == "" {
				continue
			}
			if ip, nw, err := net.ParseCIDR(a); err == nil {
				nw.IP = ip
				ifc.AltAddrs = append(ifc.AltAddrs, nw)
			}
		}
		out = append(out, ifc)
	}
	return out, nil
}
