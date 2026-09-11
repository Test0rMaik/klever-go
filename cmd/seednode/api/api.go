package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/api/httpserver"
	"github.com/klever-io/klever-go/network/api/logs"
	"github.com/klever-io/klever-go/network/api/middleware"
	"github.com/klever-io/klever-go/network/api/shared"
	wsocket "github.com/klever-io/klever-go/network/api/websocket"
	"github.com/klever-io/klever-go/tools/marshal"
	clientSocket "github.com/klever-io/klever-go/websocket"
)

var log = logger.GetOrCreate("seednode/api")

// logWSMaxConnections caps live seednode /log connections, node-wide, with no per-IP dimension
// and no knob: the seednode has no webServer antiflood section to read one from, and every live
// session registers a process-global observer that formats every log line, so unbounded is the
// wrong default for a route nobody needs tens of. It is the node's built-in fallback, for the
// same reason.
const logWSMaxConnections = 32

// Route package keys, config route names, and served URL paths, kept as constants to avoid
// duplicating the string literals across registration and the fail-safe default.
const (
	logPackage   = "log"
	peersPackage = "peers"
	nodePackage  = "node"

	logRoute     = "/log"
	peersRoute   = "/peers"
	statusRoute  = "/status"
	metricsRoute = "/metrics"

	nodeStatusPath  = "/node/status"
	nodeMetricsPath = "/node/metrics"
)

// peerInfoProvider is the narrow surface the seednode API needs from the p2p
// messenger. Kept inside this package so handlers can be tested without
// pulling in the full libp2p stack.
type peerInfoProvider interface {
	Peers() []core.PeerID
	Addresses() []string
	ConnectedAddresses() []string
}

// server bundles the state needed to serve the seednode HTTP surface.
type server struct {
	marshalizer  marshal.Marshalizer
	messenger    peerInfoProvider
	version      string
	startTime    time.Time
	routesConfig config.APIRoutesConfig
}

// Start boots the gin server with the seednode routes. messenger and version
// are exposed by the /peers, /node/status and /node/metrics endpoints.
// startTime should reflect process start so uptime reflects the binary, not
// the API listener.
func Start(restAPIInterface string, marshalizer marshal.Marshalizer, messenger peerInfoProvider, version string, startTime time.Time, routesConfig config.APIRoutesConfig) error {
	srv := &server{
		marshalizer:  marshalizer,
		messenger:    messenger,
		version:      version,
		startTime:    startTime,
		routesConfig: routesConfig,
	}

	gin.SetMode(gin.ReleaseMode)

	// gin.New skips the access logger so /node/metrics scrapes don't spam stdout.
	ws := gin.New()
	ws.Use(gin.Recovery())
	ws.Use(cors.Default())

	srv.registerRoutes(ws)

	// Hardened http.Server instead of ws.Run: adds the ReadHeaderTimeout that
	// http.ListenAndServe lacks (slow-header DoS, GHSA-w4c6-7r69-w7j9).
	return httpserver.NewHardenedServer(restAPIInterface, ws.Handler()).ListenAndServe()
}

func (s *server) registerRoutes(ws *gin.Engine) {
	if s.routesConfig.IsRouteEnabled(logPackage, logRoute) {
		s.registerLoggerWsRoute(ws)
	}
	s.registerGet(ws, peersPackage, peersRoute, peersRoute, s.peers)
	s.registerGet(ws, nodePackage, statusRoute, nodeStatusPath, s.nodeStatus)
	s.registerGet(ws, nodePackage, metricsRoute, nodeMetricsPath, s.nodeMetrics)
}

// registerGet registers a GET endpoint when its config route (pkg/configName) is open, prepending
// Basic Authentication when that route is marked secured. path is the URL gin actually serves.
func (s *server) registerGet(ws *gin.Engine, pkg, configName, path string, handler gin.HandlerFunc) {
	if !s.routesConfig.IsRouteEnabled(pkg, configName) {
		return
	}

	handlers := []gin.HandlerFunc{handler}
	if s.routesConfig.IsRouteSecured(pkg, configName) {
		handlers = append([]gin.HandlerFunc{middleware.NewAuthenticationFunc(s.routesConfig)}, handlers...)
	}

	ws.GET(path, handlers...)
}

func (s *server) registerLoggerWsRoute(ws *gin.Engine) {
	// Built once and never mutated afterwards: assigning CheckOrigin per request raced with
	// Upgrade reading it whenever two clients dialled /log at the same time. The seednode has no
	// allowlist config, so the nil list applies the node's default: non-browser clients (no
	// Origin header) are allowed, every browser origin is rejected. /log ships secured, and
	// secured also enables profile application, so an unconditional CheckOrigin let a page an
	// operator visited stream seednode logs on cached Basic credentials and mute the
	// process-global logger (CSWSH, CWE-1385).
	upgrader := wsocket.NewUpgrader(nil)
	limiter := wsocket.NewConnLimiter(logWSMaxConnections, 0)
	// Rejected upgrades are peer-driven — a page an operator visits can retry them at will on
	// cached credentials — so they share one budget: one line per window with the count.
	upgradeFailWarn := clientSocket.NewDropWarner(clientSocket.PeerDrivenLogWindow)

	// Only an authenticated (secured) /log may apply a client-supplied logger profile to the
	// process-global logger; on an unauthenticated /log profiles are ignored (GHSA-9v8p-frvj-2pcm).
	secured := s.routesConfig.IsRouteSecured(logPackage, logRoute)

	logHandler := func(c *gin.Context) {
		// Auth runs before this handler, so an unauthenticated peer takes no slot.
		release, ok := limiter.Acquire(wsocket.RemoteIP(c.Request))
		if !ok {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, map[string]string{"error": "too many websocket connections"})
			return
		}
		// Streaming stays on the request goroutine — the seednode has no global throttler
		// whose slot would need releasing — so this deferred release covers every exit,
		// including a panic, which gin.Recovery contains here.
		defer release()

		conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
		if err != nil {
			if count, ok := upgradeFailWarn.Fire(); ok {
				log.Warn("/log websocket upgrade failed", "error", shared.QuoteForLog(err.Error()), "similarSinceLastLog", count)
			}
			return
		}

		ls, err := logs.NewLogSender(s.marshalizer, conn, log, secured)
		if err != nil {
			// Past Upgrade the socket is hijacked; nothing else will close it.
			_ = conn.Close()
			log.Error("/log cannot create log sender", "error", shared.QuoteForLog(err.Error()))
			return
		}

		ls.StartSendingBlocking()
	}

	handlers := []gin.HandlerFunc{logHandler}
	if secured {
		// GHSA-9v8p-frvj-2pcm / KLC-2438: enforce authentication before the WebSocket
		// upgrade. Without this the seednode /log stream was always unauthenticated.
		handlers = append([]gin.HandlerFunc{middleware.NewAuthenticationFunc(s.routesConfig)}, handlers...)
	}

	ws.GET(logRoute, handlers...)
}

// DefaultRoutesConfig is the fail-safe used when no API config file can be loaded: the read-only
// monitoring endpoints stay enabled so observability never silently breaks, while /log (which can
// stream logs and, when secured, mutate the logger profile) stays disabled rather than being
// exposed unauthenticated.
func DefaultRoutesConfig() config.APIRoutesConfig {
	return config.APIRoutesConfig{
		APIPackages: map[string]config.APIPackageConfig{
			peersPackage: {Routes: []config.RouteConfig{{Name: peersRoute, Open: true}}},
			nodePackage: {Routes: []config.RouteConfig{
				{Name: statusRoute, Open: true},
				{Name: metricsRoute, Open: true},
			}},
		},
	}
}

// peerSnapshot: connected count matches its slice; knownPeers is a separate read and may drift under churn.
type peerSnapshot struct {
	connectedAddrs []string
	knownPeers     int
	listenAddrs    []string
}

func (s *server) snapshot() peerSnapshot {
	connected := append([]string(nil), s.messenger.ConnectedAddresses()...)
	sort.Strings(connected)
	return peerSnapshot{
		connectedAddrs: connected,
		knownPeers:     len(s.messenger.Peers()),
		listenAddrs:    s.messenger.Addresses(),
	}
}

type peersResponse struct {
	ConnectedPeers     int      `json:"connectedPeers"`
	KnownPeers         int      `json:"knownPeers"`
	ListenAddresses    []string `json:"listenAddresses"`
	ConnectedAddresses []string `json:"connectedAddresses"`
}

func (s *server) peers(c *gin.Context) {
	snap := s.snapshot()
	c.JSON(http.StatusOK, peersResponse{
		ConnectedPeers:     len(snap.connectedAddrs),
		KnownPeers:         snap.knownPeers,
		ListenAddresses:    snap.listenAddrs,
		ConnectedAddresses: snap.connectedAddrs,
	})
}

type nodeStatusResponse struct {
	Version         string   `json:"version"`
	UptimeSeconds   uint64   `json:"uptimeSeconds"`
	ConnectedPeers  int      `json:"connectedPeers"`
	KnownPeers      int      `json:"knownPeers"`
	ListenAddresses []string `json:"listenAddresses"`
}

func (s *server) nodeStatus(c *gin.Context) {
	snap := s.snapshot()
	c.JSON(http.StatusOK, nodeStatusResponse{
		Version:         s.version,
		UptimeSeconds:   uint64(time.Since(s.startTime).Seconds()),
		ConnectedPeers:  len(snap.connectedAddrs),
		KnownPeers:      snap.knownPeers,
		ListenAddresses: snap.listenAddrs,
	})
}

var prometheusLabelEscaper = strings.NewReplacer(
	`\`, `\\`,
	"\n", `\n`,
	`"`, `\"`,
)

func (s *server) nodeMetrics(c *gin.Context) {
	snap := s.snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "seednode_connected_peers %d\n", len(snap.connectedAddrs))
	fmt.Fprintf(&b, "seednode_known_peers %d\n", snap.knownPeers)
	fmt.Fprintf(&b, "seednode_uptime_seconds %d\n", uint64(time.Since(s.startTime).Seconds()))
	if s.version != "" {
		fmt.Fprintf(&b, "klv_build_info{version=\"%s\",node_type=\"seednode\"} 1\n",
			prometheusLabelEscaper.Replace(s.version))
	}
	c.String(http.StatusOK, b.String())
}
