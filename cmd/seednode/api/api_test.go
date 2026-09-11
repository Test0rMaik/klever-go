package api

import (
	"bufio"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/api/httpserver"
	"github.com/klever-io/klever-go/tools/marshal"
)

type stubMessenger struct {
	peers       []core.PeerID
	listenAddrs []string
	connected   []string
}

func (s *stubMessenger) Peers() []core.PeerID         { return s.peers }
func (s *stubMessenger) Addresses() []string          { return s.listenAddrs }
func (s *stubMessenger) ConnectedAddresses() []string { return s.connected }

func newTestServer(stub *stubMessenger, version string, started time.Time) *server {
	return &server{
		messenger:    stub,
		version:      version,
		startTime:    started,
		routesConfig: DefaultRoutesConfig(),
	}
}

func setup(t *testing.T) (*gin.Engine, *stubMessenger) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	stub := &stubMessenger{
		peers:       []core.PeerID{"peer-a", "peer-b", "peer-c"},
		listenAddrs: []string{"/ip4/127.0.0.1/tcp/1/p2p/A", "/ip4/10.0.0.1/tcp/1/p2p/A"},
		connected:   []string{"/ip4/2.2.2.2/tcp/1/p2p/Y", "/ip4/1.1.1.1/tcp/1/p2p/X"},
	}
	srv := newTestServer(stub, "v1.2.3", time.Now().Add(-90*time.Second))

	r := gin.New()
	srv.registerRoutes(r)
	return r, stub
}

// TestSeednodeAPI_HardenedServerDropsSlowHeader serves the real seednode routes through
// NewHardenedServer and confirms the seednode listener (the reporter's PoC path) drops a
// slow-header connection — GHSA-w4c6-7r69-w7j9, verified end-to-end, not just wired.
func TestSeednodeAPI_HardenedServerDropsSlowHeader(t *testing.T) {
	r, _ := setup(t)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	srv := httpserver.NewHardenedServer(ln.Addr().String(), r.Handler())
	srv.ReadHeaderTimeout = 200 * time.Millisecond // tighten for a fast test
	go func() { _ = srv.Serve(ln) }()
	defer func() { _ = srv.Close() }()

	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer func() { _ = conn.Close() }()

	// Partial header, never terminated: the seednode listener must drop it.
	if _, err := conn.Write([]byte("GET /node/status HTTP/1.1\r\nHost: x\r\n")); err != nil {
		t.Fatalf("write: %v", err)
	}

	start := time.Now()
	_ = conn.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, _ = bufio.NewReader(conn).ReadString('\n')
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("slow-header connection not dropped promptly: %v", elapsed)
	}
}

func seedLogRoutesConfig(open, secured bool) config.APIRoutesConfig {
	return config.APIRoutesConfig{
		APIPackages: map[string]config.APIPackageConfig{
			"log": {Routes: []config.RouteConfig{{Name: "/log", Open: open, Secured: secured}}},
		},
		Credentials: []config.Credential{{Username: "user", Password: "deadbeef"}},
		Hasher:      config.TypeConfig{Type: "sha256"},
	}
}

// TestLogRoute_SecuredRequiresAuth verifies GHSA-9v8p-frvj-2pcm / KLC-2438: when /log is secured,
// the seednode rejects an unauthenticated request before the WebSocket upgrade.
func TestLogRoute_SecuredRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &server{
		marshalizer:  &marshal.ProtoMarshalizer{},
		messenger:    &stubMessenger{},
		routesConfig: seedLogRoutesConfig(true, true),
	}
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (auth required before upgrade)", w.Code)
	}
}

// TestLogRoute_UnsecuredReachesUpgrade confirms that an open-but-unsecured /log has no auth gate:
// the request reaches the gorilla upgrader, which rejects the non-WebSocket GET with 400.
func TestLogRoute_UnsecuredReachesUpgrade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &server{
		marshalizer:  &marshal.ProtoMarshalizer{},
		messenger:    &stubMessenger{},
		routesConfig: seedLogRoutesConfig(true, false),
	}
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 (reaches upgrader, no auth gate)", w.Code)
	}
}

// TestLogRoute_BrowserOriginRejected covers the seednode CSWSH guard (CWE-1385): /log ships
// secured, and secured also enables profile application, so a page an operator visits must not
// be able to open the socket on cached Basic credentials. A request carrying an Origin is a
// browser and is rejected; one without is a non-browser client and passes the origin check
// (then fails on the missing Sec-WebSocket-Key, which is past the check).
func TestLogRoute_BrowserOriginRejected(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &server{
		marshalizer:  &marshal.ProtoMarshalizer{},
		messenger:    &stubMessenger{},
		routesConfig: seedLogRoutesConfig(true, false),
	}
	r := gin.New()
	srv.registerRoutes(r)

	for _, tc := range []struct {
		origin string
		want   int
	}{
		{origin: "https://evil.example", want: http.StatusForbidden},
		{origin: "null", want: http.StatusForbidden},
		{origin: "", want: http.StatusBadRequest},
	} {
		req := httptest.NewRequest(http.MethodGet, "/log", nil)
		req.Header.Set("Connection", "upgrade")
		req.Header.Set("Upgrade", "websocket")
		req.Header.Set("Sec-Websocket-Version", "13")
		if tc.origin != "" {
			req.Header.Set("Origin", tc.origin)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != tc.want {
			t.Errorf("Origin %q: status = %d, want %d", tc.origin, w.Code, tc.want)
		}
	}
}

// TestLogRoute_DisabledNotRegistered confirms /log is not registered when not open (fail-safe
// default when the seednode has no API config).
func TestLogRoute_DisabledNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &server{marshalizer: &marshal.ProtoMarshalizer{}, messenger: &stubMessenger{}}
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (/log not registered)", w.Code)
	}
}

// TestMonitoringRoute_SecuredRequiresAuth verifies the per-route auth gate also applies to the
// monitoring endpoints: a secured /node/status rejects an unauthenticated request.
func TestMonitoringRoute_SecuredRequiresAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &server{
		messenger: &stubMessenger{},
		routesConfig: config.APIRoutesConfig{
			APIPackages: map[string]config.APIPackageConfig{
				"node": {Routes: []config.RouteConfig{{Name: "/status", Open: true, Secured: true}}},
			},
			Credentials: []config.Credential{{Username: "user", Password: "deadbeef"}},
			Hasher:      config.TypeConfig{Type: "sha256"},
		},
	}
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/node/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401 (secured monitoring route)", w.Code)
	}
}

// TestMonitoringRoute_DisabledNotRegistered confirms a route with open:false is not served.
func TestMonitoringRoute_DisabledNotRegistered(t *testing.T) {
	gin.SetMode(gin.TestMode)
	srv := &server{
		messenger: &stubMessenger{},
		routesConfig: config.APIRoutesConfig{
			APIPackages: map[string]config.APIPackageConfig{
				"peers": {Routes: []config.RouteConfig{{Name: "/peers", Open: false}}},
			},
		},
	}
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/peers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (/peers disabled)", w.Code)
	}
}

// TestDefaultRoutesConfig confirms the fail-safe surface: monitoring on, /log off.
func TestDefaultRoutesConfig(t *testing.T) {
	cfg := DefaultRoutesConfig()
	if !cfg.IsRouteEnabled("peers", "/peers") {
		t.Error("/peers should be enabled by default")
	}
	if !cfg.IsRouteEnabled("node", "/status") {
		t.Error("/node/status should be enabled by default")
	}
	if !cfg.IsRouteEnabled("node", "/metrics") {
		t.Error("/node/metrics should be enabled by default")
	}
	if cfg.IsRouteEnabled("log", "/log") {
		t.Error("/log must be disabled by default (fail-safe)")
	}
}

func TestPeers_returnsCountsAndSortedAddresses(t *testing.T) {
	r, _ := setup(t)

	req := httptest.NewRequest(http.MethodGet, "/peers", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got peersResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ConnectedPeers != 2 {
		t.Errorf("connectedPeers = %d, want 2", got.ConnectedPeers)
	}
	if got.KnownPeers != 3 {
		t.Errorf("knownPeers = %d, want 3", got.KnownPeers)
	}
	if len(got.ConnectedAddresses) != 2 || got.ConnectedAddresses[0] >= got.ConnectedAddresses[1] {
		t.Errorf("connectedAddresses not sorted: %v", got.ConnectedAddresses)
	}
	if len(got.ListenAddresses) != 2 {
		t.Errorf("listenAddresses = %v, want 2 entries", got.ListenAddresses)
	}
}

func TestNodeStatus_reportsUptimeAndVersion(t *testing.T) {
	r, _ := setup(t)

	req := httptest.NewRequest(http.MethodGet, "/node/status", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	var got nodeStatusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Version != "v1.2.3" {
		t.Errorf("version = %q, want v1.2.3", got.Version)
	}
	if got.UptimeSeconds < 89 || got.UptimeSeconds > 120 {
		t.Errorf("uptimeSeconds = %d, want ~90", got.UptimeSeconds)
	}
	if got.ConnectedPeers != 2 || got.KnownPeers != 3 {
		t.Errorf("counts = (%d,%d), want (2,3)", got.ConnectedPeers, got.KnownPeers)
	}
}

func TestNodeMetrics_prometheusFormat(t *testing.T) {
	r, _ := setup(t)

	req := httptest.NewRequest(http.MethodGet, "/node/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}

	body := w.Body.String()
	wantLines := []string{
		"seednode_connected_peers 2",
		"seednode_known_peers 3",
		`klv_build_info{version="v1.2.3",node_type="seednode"} 1`,
	}
	for _, want := range wantLines {
		if !strings.Contains(body, want) {
			t.Errorf("metrics body missing %q\nfull body:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "seednode_uptime_seconds ") {
		t.Errorf("metrics body missing seednode_uptime_seconds line\nfull body:\n%s", body)
	}
}

func TestNodeMetrics_emptyVersionOmitsBuildInfo(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &stubMessenger{}
	srv := newTestServer(stub, "", time.Now())
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/node/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	if strings.Contains(w.Body.String(), "klv_build_info") {
		t.Errorf("expected klv_build_info to be omitted when version empty, got:\n%s", w.Body.String())
	}
}

func TestNodeMetrics_versionLabelEscaped(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &stubMessenger{}
	srv := newTestServer(stub, `v"weird\ne`+"\n"+`xt`, time.Now())
	r := gin.New()
	srv.registerRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/node/metrics", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	body := w.Body.String()
	// If escaping worked, every newline in the body is a line terminator —
	// no raw newline leaked from the version label into the middle of a line.
	if got, want := strings.Count(body, "\n"), countNonEmptyLines(body); got != want {
		t.Errorf("escaping leaked a raw newline into the label: \\n count = %d, lines = %d, body:\n%s", got, want, body)
	}
	if !strings.Contains(body, `\"`) || !strings.Contains(body, `\\`) {
		t.Errorf("expected escaped quote and backslash in label, body:\n%s", body)
	}
}

func TestSnapshot_doesNotMutateMessengerSlices(t *testing.T) {
	gin.SetMode(gin.TestMode)
	stub := &stubMessenger{
		connected: []string{"/ip4/9/tcp/1/p2p/Z", "/ip4/1/tcp/1/p2p/A", "/ip4/5/tcp/1/p2p/M"},
	}
	srv := newTestServer(stub, "", time.Now())

	wantConnected := append([]string(nil), stub.connected...)

	for range 3 {
		_ = srv.snapshot()
	}

	for i, addr := range stub.connected {
		if addr != wantConnected[i] {
			t.Fatalf("snapshot mutated stub.connected[%d]: got %q, want %q (full got: %v)",
				i, addr, wantConnected[i], stub.connected)
		}
	}
}

func countNonEmptyLines(body string) int {
	n := 0
	for line := range strings.SplitSeq(body, "\n") {
		if line != "" {
			n++
		}
	}
	return n
}

// startSeednodeLogServer serves the seednode's open, unsecured /log over a real socket so a
// test can dial it with a websocket client.
func startSeednodeLogServer(t *testing.T, marshalizer marshal.Marshalizer) string {
	t.Helper()

	gin.SetMode(gin.TestMode)
	srv := &server{
		marshalizer:  marshalizer,
		messenger:    &stubMessenger{},
		routesConfig: seedLogRoutesConfig(true, false),
	}
	r := gin.New()
	srv.registerRoutes(r)

	hs := httptest.NewServer(r)
	t.Cleanup(hs.Close)

	return hs.Listener.Addr().String()
}

func dialSeednodeLog(addr string, header http.Header) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial("ws://"+addr+"/log", header)
}

// fillSeednodeLogCap opens and handshakes logWSMaxConnections sessions and returns them; the
// caller owns their closing.
func fillSeednodeLogCap(t *testing.T, addr string) []*websocket.Conn {
	t.Helper()

	conns := make([]*websocket.Conn, 0, logWSMaxConnections)
	for i := 0; i < logWSMaxConnections; i++ {
		conn, _, err := dialSeednodeLog(addr, nil)
		if err != nil {
			t.Fatalf("dial %d within the cap: %v", i, err)
		}
		if err := conn.WriteMessage(websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier)); err != nil {
			t.Fatalf("handshake %d: %v", i, err)
		}
		conns = append(conns, conn)
	}

	return conns
}

func expectSeednodeLogRefused(t *testing.T, addr string) {
	t.Helper()

	_, resp, err := dialSeednodeLog(addr, nil)
	if err == nil {
		t.Fatal("a dial beyond the cap must be refused")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("beyond the cap: resp = %v, want 503", resp)
	}
}

// TestLogRoute_ConnectionCap covers the seednode's built-in cap: it has no config to size one
// from, so live /log sessions are bounded at logWSMaxConnections and the next dial is refused.
func TestLogRoute_ConnectionCap(t *testing.T) {
	addr := startSeednodeLogServer(t, &marshal.ProtoMarshalizer{})

	conns := fillSeednodeLogCap(t, addr)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})

	expectSeednodeLogRefused(t, addr)
}

// TestLogRoute_ConnectionCapIsReleasedOnClose is the other half of a cap: a session that ends
// must hand its slot back, or the route fills up once and stays full. Filling the cap alone
// cannot see a deleted release.
func TestLogRoute_ConnectionCapIsReleasedOnClose(t *testing.T) {
	addr := startSeednodeLogServer(t, &marshal.ProtoMarshalizer{})

	conns := fillSeednodeLogCap(t, addr)
	t.Cleanup(func() {
		for _, c := range conns {
			_ = c.Close()
		}
	})
	expectSeednodeLogRefused(t, addr)

	if err := conns[0].Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		reconnected, _, err := dialSeednodeLog(addr, nil)
		if err == nil {
			_ = reconnected.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("slot must be released once the session tears down; the cap is stuck")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestLogRoute_RejectedUpgradeReleasesSlot: a dial refused at the origin check takes a slot
// before the upgrade and must give it back, or cap+1 rejected browsers lock every operator out.
func TestLogRoute_RejectedUpgradeReleasesSlot(t *testing.T) {
	addr := startSeednodeLogServer(t, &marshal.ProtoMarshalizer{})

	for i := 0; i < logWSMaxConnections+1; i++ {
		_, resp, err := dialSeednodeLog(addr, http.Header{"Origin": []string{"https://evil.example"}})
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("dial %d: want 403, got err=%v resp=%v", i, err, resp)
		}
	}

	conn, _, err := dialSeednodeLog(addr, nil)
	if err != nil {
		t.Fatalf("rejected dials must hand their slot back, leaving the cap free: %v", err)
	}
	_ = conn.Close()
}

// TestLogRoute_UpgradeFailuresAreBudgeted counts what the logger emits: twenty origin-rejected
// dials, one line. A page an operator visits can drive these at will on cached credentials.
func TestLogRoute_UpgradeFailuresAreBudgeted(t *testing.T) {
	counter := mock.NewLogLineCounter("/log websocket upgrade failed")
	if err := logger.AddLogObserver(counter, counter); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logger.RemoveLogObserver(counter) })

	addr := startSeednodeLogServer(t, &marshal.ProtoMarshalizer{})
	for i := 0; i < 20; i++ {
		_, resp, err := dialSeednodeLog(addr, http.Header{"Origin": []string{"https://evil.example"}})
		if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
			t.Fatalf("dial %d: want 403, got err=%v resp=%v", i, err, resp)
		}
	}

	deadline := time.Now().Add(2 * time.Second)
	for counter.Count() < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	time.Sleep(50 * time.Millisecond)
	if got := counter.Count(); got != 1 {
		t.Fatalf("upgrade-failure lines = %d, want 1: the budget must fold twenty rejections into one line", got)
	}
}

// TestLogRoute_SenderFailureClosesTheSocket: past Upgrade the socket is hijacked and nothing
// but the handler will ever close it, so a sender that cannot be built must not leave it open.
func TestLogRoute_SenderFailureClosesTheSocket(t *testing.T) {
	// A nil marshalizer is the one way NewLogSender refuses a live connection.
	addr := startSeednodeLogServer(t, nil)

	conn, _, err := dialSeednodeLog(addr, nil)
	if err != nil {
		t.Fatalf("the upgrade completes before the sender is built: %v", err)
	}
	defer func() { _ = conn.Close() }()

	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _, err = conn.ReadMessage()
	if err == nil {
		t.Fatal("expected the server to close the socket")
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatal("socket left open after the sender failed to build: the read timed out instead of failing on close")
	}
}
