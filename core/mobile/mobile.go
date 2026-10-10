// Package mobile is the gomobile-facing API used by the Android apps.
// Keep the exported surface to strings, bools, ints and errors so gobind can
// translate it.
package mobile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"time"

	"nasfone/core/auth"
	"nasfone/core/pair"
	"nasfone/core/server"

	"tailscale.com/envknob"
	"tailscale.com/ipn"
	"tailscale.com/net/netmon"
	"tailscale.com/tsnet"
)

// Host is implemented on the Kotlin side.
type Host interface {
	OnStatus(statusJSON string)
	OnLog(line string)
	// OnEvent reports security events: kind is "login", "revoke" or "code_rolled".
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
	Funnel     bool   `json:"funnel"`
	Verbose    bool   `json:"verbose"`
	LANPort    int    `json:"lanPort"` // 0 = no local-network listener
	// HiddenDirs are folders kept out of the share even when RootDir contains
	// them (the Windows server passes its own data folders here).
	HiddenDirs []string `json:"hiddenDirs"`
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
	FunnelWanted bool     `json:"funnelWanted"`
	FunnelURL    string   `json:"funnelURL,omitempty"`
	FunnelError  string   `json:"funnelError,omitempty"`
	FunnelHelp   string   `json:"funnelHelpURL,omitempty"` // admin page that fixes FunnelError
	Error        string   `json:"error,omitempty"`
	LANURLs      []string `json:"lanURLs,omitempty"`
	LANError     string   `json:"lanError,omitempty"`

	// Activity, for the notification. Byte counters are cumulative since
	// start; the app turns deltas into speeds.
	OpenConns int   `json:"openConns"`
	BytesIn   int64 `json:"bytesIn"`
	BytesOut  int64 `json:"bytesOut"`
	Sessions  int   `json:"sessions"`
}

type node struct {
	cfg  Config
	host Host

	ctx    context.Context
	cancel context.CancelFunc

	ts      *tsnet.Server
	auth    *auth.Store
	pairs   *pair.Store
	lan     *server.LAN
	traffic traffic
	handler http.Handler
	servers []*http.Server

	mu             sync.Mutex
	status         Status
	lastJSON       string
	loginRequested bool
	funnelSrv      *http.Server
	funnelErrAt    time.Time
	lanSrv         *http.Server // local-network listener, nil when off
	lanPort        int
	lanIPs         string // local IPv4s last seen; a change ends LAN sessions
	lanIPsSet      bool
}

// funnelRetry is how often a failed Funnel listen is retried, so enabling
// HTTPS/Funnel in the admin console takes effect without restarting the app.
const funnelRetry = 20 * time.Second

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

// Start launches the embedded Tailscale node. The server is reachable
// through the tailnet (and Funnel when enabled), plus a local-network
// listener when cfg.LANPort is set (see lan.go).
func Start(configJSON string, host Host) error {
	var cfg Config
	if err := json.Unmarshal([]byte(configJSON), &cfg); err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.StateDir == "" || cfg.RootDir == "" {
		return errors.New("config: stateDir and rootDir are required")
	}
	if cfg.Hostname == "" {
		cfg.Hostname = "nasfone"
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
	// Paired apps and the server identity key live apart from the tailnet
	// state, so switching Tailscale accounts does not unpair anything.
	pairs, err := pair.Open(filepath.Join(filepath.Dir(cfg.StateDir), "pair"))
	if err != nil {
		return fmt.Errorf("pair store: %w", err)
	}
	pairs.OnEvent = host.OnEvent

	ctx, cancel := context.WithCancel(context.Background())
	n := &node{cfg: cfg, host: host, ctx: ctx, cancel: cancel, auth: store, pairs: pairs}
	// Always present so the LAN listener can be switched on later (SetLan).
	n.lan = server.NewLAN()
	n.lan.Logf = n.logf
	n.status = Status{FunnelWanted: cfg.Funnel, BackendState: "Starting"}
	n.handler = server.NewHandler(server.Options{
		Root: cfg.RootDir, Auth: store, Pair: pairs, LAN: n.lan, Logf: n.logf,
		Addresses: n.addresses,
		Hidden:    hiddenList(cfg.HiddenDirs),
		PublicURL: func() string {
			n.mu.Lock()
			defer n.mu.Unlock()
			return n.status.FunnelURL // "" when Funnel is off: fall back to the request address
		},
	})

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
	if cfg.LANPort > 0 {
		n.setLAN(cfg.LANPort)
	}

	current = n
	go n.loop()
	n.logf("Đã khởi động, tên máy tailnet %q", cfg.Hostname)
	return nil
}

// hiddenList turns the configured hidden folders into server.Options.Hidden
// (nil when there are none, so nothing is checked).
func hiddenList(dirs []string) func() []string {
	if len(dirs) == 0 {
		return nil
	}
	return func() []string { return dirs }
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
	n.status.FunnelHelp = ""
	n.funnelErrAt = time.Time{}
	n.mu.Unlock()
	if !enabled {
		n.closeFunnel()
	}
	n.refresh()
}

// LoginCode returns this minute's two rolling login codes, which are never equal:
// {"admin":"123 456","user":"654 321","expires":<unix ms>,"step":60}.
// "" when the server is stopped.
func LoginCode() string {
	n := get()
	if n == nil {
		return ""
	}
	admin, user, exp := n.auth.CurrentCodes()
	b, _ := json.Marshal(map[string]any{
		"admin":   admin,
		"user":    user,
		"expires": exp.UnixMilli(),
		"step":    int(auth.CodeStep.Seconds()),
	})
	return string(b)
}

// Devices returns the signed-in browser devices as a JSON array.
func Devices() string {
	n := get()
	if n == nil {
		return "[]"
	}
	b, _ := json.Marshal(append(n.auth.List(), n.lanDevices()...))
	return string(b)
}

// RevokeDevice signs one device out.
func RevokeDevice(id string) bool {
	n := get()
	if n == nil {
		return false
	}
	if lanID, ok := strings.CutPrefix(id, "lan:"); ok {
		return n.lan != nil && n.lan.Revoke(lanID)
	}
	return n.auth.Revoke(id)
}

// RevokeAllDevices signs every browser device out.
func RevokeAllDevices() {
	if n := get(); n != nil {
		n.auth.RevokeAll()
		if n.lan != nil {
			n.lan.RevokeAll()
		}
	}
}

// NetworkChanged tells the Tailscale engine that the phone's network changed
// (Wi-Fi <-> mobile data, new Wi-Fi). Android does not let apps watch routing
// changes directly, so without this nudge the engine may keep using dead
// sockets for a while. Rebinding and re-STUNing finds a new path at once.
func NetworkChanged() {
	n := get()
	if n == nil {
		return
	}
	lc, err := n.ts.LocalClient()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(n.ctx, 10*time.Second)
	defer cancel()
	for _, a := range []string{"rebind", "restun"} {
		if err := lc.DebugAction(ctx, a); err != nil {
			n.logf("Đổi mạng (%s): %v", a, err)
		}
	}
	n.logf("Mạng thay đổi, đã kết nối lại Tailscale")
	n.refresh()
}

// NewPairInvite creates a one-time invite for a client app with role "admin"
// or "user": {"invite":"nasfone1:…","link":"nasfone://pair?i=…","expires":<ms>,"fp":"ABCD-…"}.
// The invite points at the Funnel URL when Funnel is open (works anywhere),
// otherwise at the tailnet name.
func NewPairInvite(role string) (string, error) {
	n := get()
	if n == nil {
		return "", errors.New("server chưa chạy")
	}
	n.mu.Lock()
	base := n.status.FunnelURL
	if base == "" && n.status.DNSName != "" {
		base = "http://" + n.status.DNSName
	}
	n.mu.Unlock()
	if base == "" {
		return "", errors.New("chưa kết nối Tailscale nên chưa có địa chỉ để mời")
	}
	inv, exp := n.pairs.NewInvite(auth.Role(role), base)
	b, _ := json.Marshal(map[string]any{
		"invite":  inv,
		"link":    "nasfone://pair?i=" + url.QueryEscape(inv),
		"expires": exp.UnixMilli(),
		"fp":      pair.ShortFP(n.pairs.Fingerprint()),
	})
	return string(b), nil
}

// PairedDevices returns paired client apps as a JSON array.
func PairedDevices() string {
	n := get()
	if n == nil {
		return "[]"
	}
	b, _ := json.Marshal(n.pairs.List())
	return string(b)
}

// RevokePaired unpairs a client app.
func RevokePaired(id string) bool {
	n := get()
	return n != nil && n.pairs.Revoke(id)
}

// SetPairedRole changes a paired app's role ("admin" or "user").
func SetPairedRole(id, role string) bool {
	n := get()
	return n != nil && n.pairs.SetRole(id, auth.Role(role))
}

// ServerFingerprint is the short fingerprint of the server identity key.
func ServerFingerprint() string {
	n := get()
	if n == nil {
		return ""
	}
	return pair.ShortFP(n.pairs.Fingerprint())
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
	ln = n.traffic.wrap(ln)
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
		n.checkLAN()
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
	s.OpenConns = int(n.traffic.open.Load())
	s.BytesIn = n.traffic.bytesIn.Load()
	s.BytesOut = n.traffic.bytesOut.Load()
	s.Sessions = len(n.auth.List())
	wantFunnel := n.cfg.Funnel && st.BackendState == ipn.Running.String() && n.funnelSrv == nil &&
		(n.status.FunnelError == "" || time.Since(n.funnelErrAt) > funnelRetry)
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
	if err == nil {
		ln = n.traffic.wrap(ln)
	}
	n.mu.Lock()
	defer n.mu.Unlock()
	if err != nil {
		msg, help := explainFunnelError(err)
		if msg != n.status.FunnelError {
			n.logf("Funnel lỗi: %v", err)
		}
		n.status.FunnelError, n.status.FunnelHelp = msg, help
		n.funnelErrAt = time.Now()
		return
	}
	n.status.FunnelError, n.status.FunnelHelp = "", ""
	srv := &http.Server{
		Handler: server.WithVia(n.handler, "Funnel"),
		// The :443 listener serves both the public Funnel and tailnet peers
		// using the https name; tell them apart and keep the real client IP.
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if fc := funnelConnOf(c); fc != nil {
				return server.WithConnInfo(ctx, "Funnel", fc.Src.Addr().String())
			}
			return server.WithConnInfo(ctx, "Tailnet", "")
		},
		ReadHeaderTimeout: 30 * time.Second,
		// TLS handshake failures (e.g. no certificate yet) are otherwise only
		// printed to stderr, which is invisible on Android.
		ErrorLog: log.New(&throttledLog{logf: n.logf, prefix: "Funnel: ", every: 10 * time.Second}, "", 0),
	}
	n.funnelSrv = srv
	n.status.FunnelURL = "https://" + n.status.DNSName
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			n.logf("Funnel server: %v", err)
		}
	}()
	n.logf("Funnel đang mở: %s", n.status.FunnelURL)
	go n.warmCert(n.status.DNSName)
}

// warmCert fetches the Let's Encrypt certificate right away instead of on the
// first visitor's handshake, and logs how it went.
func (n *node) warmCert(domain string) {
	lc, err := n.ts.LocalClient()
	if err != nil {
		return
	}
	n.logf("Đang xin chứng chỉ HTTPS cho %s…", domain)
	t0 := time.Now()
	ctx, cancel := context.WithTimeout(n.ctx, 3*time.Minute)
	defer cancel()
	if _, _, err := lc.CertPair(ctx, domain); err != nil {
		n.logf("Chứng chỉ HTTPS lỗi sau %s: %v", time.Since(t0).Round(time.Second), err)
		return
	}
	n.logf("Đã có chứng chỉ HTTPS (%s)", time.Since(t0).Round(time.Second))
}

// throttledLog forwards log lines to logf at most once per interval.
type throttledLog struct {
	logf   func(string, ...any)
	prefix string
	every  time.Duration
	mu     sync.Mutex
	last   time.Time
	quiet  int
}

func (t *throttledLog) Write(p []byte) (int, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.last) < t.every {
		t.quiet++
		return len(p), nil
	}
	line := t.prefix + strings.TrimSpace(string(p))
	if t.quiet > 0 {
		line += fmt.Sprintf(" (+%d dòng tương tự bị ẩn)", t.quiet)
	}
	t.last, t.quiet = time.Now(), 0
	t.logf("%s", line)
	return len(p), nil
}

// explainFunnelError turns tailscale's Funnel errors into Vietnamese guidance
// plus the admin console page where the owner can fix it.
func explainFunnelError(err error) (msg, helpURL string) {
	e := err.Error()
	switch {
	case strings.Contains(e, "HTTPS must be enabled"):
		return "Tailnet chưa bật HTTPS Certificates. Vào trang DNS của Tailscale, bật \"HTTPS Certificates\".",
			"https://login.tailscale.com/admin/dns"
	case strings.Contains(e, `"funnel" node attribute not set`):
		return "Máy này chưa được cấp quyền Funnel. Trong Access controls, thêm nodeAttrs \"funnel\" cho autogroup:member.",
			"https://login.tailscale.com/admin/acls/file"
	case strings.Contains(e, "not allowed for funnel"):
		return "Cổng 443 chưa được phép dùng Funnel (" + e + ").", "https://login.tailscale.com/admin/acls/file"
	}
	return e, ""
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
