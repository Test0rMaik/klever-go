package api

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	logger "github.com/klever-io/klever-go-logger"
	commonmock "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/api/middleware"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func logRoutesConfig(secured bool) config.APIRoutesConfig {
	return config.APIRoutesConfig{
		APIPackages: map[string]config.APIPackageConfig{
			"log": {Routes: []config.RouteConfig{{Name: "/log", Open: true, Secured: secured}}},
		},
		Credentials: []config.Credential{{Username: "user", Password: "deadbeef"}},
		Hasher:      config.TypeConfig{Type: "sha256"},
	}
}

func startLogRouteServer(t *testing.T, maxConns uint32, maxConnsPerIP uint32, allowedOrigins ...string) string {
	t.Helper()

	ws := gin.New()
	registerLoggerWsRoute(ws, &marshal.ProtoMarshalizer{}, logRoutesConfig(false), maxConns, maxConnsPerIP, allowedOrigins)

	srv := httptest.NewServer(ws)
	t.Cleanup(srv.Close)

	return srv.Listener.Addr().String()
}

func dialLogRoute(addr string) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial("ws://"+addr+"/log", nil)
}

func dialLogRouteWithOrigin(addr string, origin string) (*websocket.Conn, *http.Response, error) {
	return websocket.DefaultDialer.Dial("ws://"+addr+"/log", http.Header{"Origin": []string{origin}})
}

func dialLogRouteAndHandshake(t *testing.T, addr string) *websocket.Conn {
	t.Helper()

	conn, _, err := dialLogRoute(addr)
	require.NoError(t, err)
	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier)))

	return conn
}

func TestIsLogRouteSecured(t *testing.T) {
	t.Parallel()

	assert.True(t, logRoutesConfig(true).IsRouteSecured("log", "/log"))
	assert.False(t, logRoutesConfig(false).IsRouteSecured("log", "/log"))
	assert.False(t, config.APIRoutesConfig{}.IsRouteSecured("log", "/log"))
}

// TestRegisterLoggerWsRoute_SecuredRequiresAuth verifies GHSA-9v8p-frvj-2pcm / KLC-2438:
// when /log is secured, an unauthenticated request is rejected before the WebSocket upgrade.
func TestRegisterLoggerWsRoute_SecuredRequiresAuth(t *testing.T) {
	t.Parallel()

	ws := gin.New()
	registerLoggerWsRoute(ws, &marshal.ProtoMarshalizer{}, logRoutesConfig(true), 0, 0, nil)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusUnauthorized, resp.Code)
}

// TestRegisterLoggerWsRoute_UnsecuredReachesUpgrade confirms an unsecured /log has no auth
// gate: the request reaches the gorilla upgrader, which rejects the non-WebSocket GET with 400.
func TestRegisterLoggerWsRoute_UnsecuredReachesUpgrade(t *testing.T) {
	t.Parallel()

	ws := gin.New()
	registerLoggerWsRoute(ws, &marshal.ProtoMarshalizer{}, logRoutesConfig(false), 0, 0, nil)

	req := httptest.NewRequest(http.MethodGet, "/log", nil)
	resp := httptest.NewRecorder()
	ws.ServeHTTP(resp, req)

	assert.Equal(t, http.StatusBadRequest, resp.Code)
}

func TestRegisterLoggerWsRoute_GlobalConnectionCap(t *testing.T) {
	addr := startLogRouteServer(t, 1, 0)

	conn := dialLogRouteAndHandshake(t, addr)
	defer func() { _ = conn.Close() }()

	_, resp, err := dialLogRoute(addr)
	require.Error(t, err, "connection beyond the global cap must be rejected")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestRegisterLoggerWsRoute_PerIPConnectionCap exercises the per-IP dimension on its own: the
// node-wide cap resolves to the built-in default (32), so with two dials only
// logWebSocketConnectionsPerIP can reject the second.
func TestRegisterLoggerWsRoute_PerIPConnectionCap(t *testing.T) {
	addr := startLogRouteServer(t, 0, 1)

	conn := dialLogRouteAndHandshake(t, addr)
	defer func() { _ = conn.Close() }()

	_, resp, err := dialLogRoute(addr)
	require.Error(t, err, "second connection from the same IP must be rejected")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

// TestRegisterLoggerWsRoute_Origin covers the CSWSH guard. Non-browser clients send no Origin
// and must keep working; a browser origin is admitted only when the operator listed it, so the
// empty default keeps a page the operator visits from streaming node logs on their credentials.
func TestRegisterLoggerWsRoute_Origin(t *testing.T) {
	t.Run("no origin header is allowed", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0)

		conn, _, err := dialLogRoute(addr)
		require.NoError(t, err)
		_ = conn.Close()
	})

	t.Run("unlisted origin is rejected", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0)

		_, resp, err := dialLogRouteWithOrigin(addr, "https://evil.example.com")
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	t.Run("listed origin is allowed", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0, "https://ops.example.com")

		conn, _, err := dialLogRouteWithOrigin(addr, "https://ops.example.com")
		require.NoError(t, err)
		_ = conn.Close()
	})

	t.Run("any origin is rejected when the allowlist is empty", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0)

		_, resp, err := dialLogRouteWithOrigin(addr, "http://localhost:3000")
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	// A sandboxed iframe or a cross-origin redirect sends the literal "null". It is an origin
	// like any other, so it is admitted only if an operator listed it — which nothing here does.
	t.Run("null origin is rejected", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0)

		_, resp, err := dialLogRouteWithOrigin(addr, "null")
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	// Scheme and host are case-insensitive, so a browser that upper-cases them is the same
	// origin the operator listed; AllowedOriginChecker lowercases both sides for this.
	t.Run("listed origin matches case-insensitively", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0, "https://ops.example.com")

		conn, _, err := dialLogRouteWithOrigin(addr, "HTTPS://OPS.EXAMPLE.COM")
		require.NoError(t, err)
		_ = conn.Close()
	})

	// RFC 6454 origin serialization carries no path, so a browser never sends the trailing
	// slash. Matching it would only widen the allowlist past what an operator wrote.
	t.Run("trailing slash is not the listed origin", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0, "https://ops.example.com")

		_, resp, err := dialLogRouteWithOrigin(addr, "https://ops.example.com/")
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	// Header.Get reports an explicitly empty Origin the same as an absent one, which would
	// have admitted it as a non-browser client. No browser sends an empty Origin; it is
	// refused, not treated as absent.
	t.Run("explicitly empty origin is rejected", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0)

		_, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/log", http.Header{"Origin": []string{""}})
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	// Header.Get also returns only the first value, so a listed origin followed by an
	// unlisted one would have passed on the first alone. Two values is not a browser either.
	t.Run("duplicate origin headers are rejected even when the first is listed", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0, "https://ops.example.com")

		_, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/log",
			http.Header{"Origin": []string{"https://ops.example.com", "https://evil.example.com"}})
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})

	// An operator who lists "" must not thereby admit the empty value: the explicit check
	// refuses it before the allowlist is consulted. Without that check this case passes only
	// because an empty allowlist happens to reject everything.
	t.Run("explicitly empty origin is rejected even when listed", func(t *testing.T) {
		addr := startLogRouteServer(t, 0, 0, "")

		_, resp, err := websocket.DefaultDialer.Dial("ws://"+addr+"/log", http.Header{"Origin": []string{""}})
		require.Error(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	})
}

// TestRegisterLoggerWsRoute_StreamingReleasesTheGlobalThrottlerSlot is the KLR-60 finding
// itself: /log used to stream inside the request goroutine, so the global throttler slot taken
// before c.Next() was held for the life of the connection and enough tailers starved every other
// route with 429s. With a one-slot throttler, an open /log must leave that slot free.
func TestRegisterLoggerWsRoute_StreamingReleasesTheGlobalThrottlerSlot(t *testing.T) {
	ws := gin.New()
	throttler, err := middleware.NewGlobalThrottler(1)
	require.NoError(t, err)
	ws.Use(throttler.MiddlewareHandlerFunc())
	ws.GET("/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	registerLoggerWsRoute(ws, &marshal.ProtoMarshalizer{}, logRoutesConfig(false), 0, 0, nil)

	srv := httptest.NewServer(ws)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()

	conn := dialLogRouteAndHandshake(t, addr)
	defer func() { _ = conn.Close() }()

	client := &http.Client{Timeout: 2 * time.Second}
	require.Eventually(t, func() bool {
		resp, err := client.Get("http://" + addr + "/ping")
		if err != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 2*time.Second, 20*time.Millisecond,
		"a live /log must not hold the global throttler slot; unrelated requests were getting 429")
}

// TestRegisterLoggerWsRoute_RejectedUpgradeReleasesSlot covers the release() on the failed-upgrade
// path. Every other test in this file dials against a limiter that is already saturated or closed
// immediately, so deleting that release() left them all green — while N rejected dials would
// permanently exhaust the cap, which is exactly the regression this suite exists to catch.
func TestRegisterLoggerWsRoute_RejectedUpgradeReleasesSlot(t *testing.T) {
	addr := startLogRouteServer(t, 1, 0)

	// Each of these takes a slot, then fails the upgrade on the origin check.
	for i := 0; i < 3; i++ {
		_, resp, err := dialLogRouteWithOrigin(addr, "https://evil.example.com")
		require.Error(t, err)
		require.NotNil(t, resp)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}

	conn, _, err := dialLogRoute(addr)
	require.NoError(t, err, "rejected dials must hand their slot back, leaving the cap free")
	_ = conn.Close()
}

// TestRegisterLoggerWsRoute_ZeroMaxConnsUsesDefault covers the unmigrated-config path: a
// config.yaml predating logWebSocketConnections yields 0, which must fall back to the built-in
// cap rather than to "unlimited". Before the cap existed, streaming blocked inside the request
// goroutine and simultaneousRequests bounded live /log connections; the async route releases
// that slot at the upgrade, so an unlimited 0 would leave an unmigrated node strictly weaker.
func TestRegisterLoggerWsRoute_ZeroMaxConnsUsesDefault(t *testing.T) {
	addr := startLogRouteServer(t, 0, 0)

	conns := make([]*websocket.Conn, 0, defaultLogWSMaxConnections)
	for i := 0; i < defaultLogWSMaxConnections; i++ {
		conns = append(conns, dialLogRouteAndHandshake(t, addr))
	}
	defer func() {
		for _, conn := range conns {
			_ = conn.Close()
		}
	}()

	_, resp, err := dialLogRoute(addr)
	require.Error(t, err, "0 must resolve to the built-in cap, not to unlimited")
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)
}

func TestRegisterLoggerWsRoute_ConnectionCapReleasedOnClose(t *testing.T) {
	addr := startLogRouteServer(t, 1, 0)

	conn := dialLogRouteAndHandshake(t, addr)

	_, resp, err := dialLogRoute(addr)
	require.Error(t, err)
	require.NotNil(t, resp)
	require.Equal(t, http.StatusServiceUnavailable, resp.StatusCode)

	require.NoError(t, conn.Close())

	require.Eventually(t, func() bool {
		reconnected, _, derr := dialLogRoute(addr)
		if derr != nil {
			return false
		}
		_ = reconnected.Close()
		return true
	}, 5*time.Second, 50*time.Millisecond, "slot must be released once the streaming goroutine tears down")
}

// TestRegisterLoggerWsRoute_UpgradeFailuresAreBudgeted counts what the logger emits: twenty
// origin-rejected dials, one line. A page an operator visits can drive these at will on cached
// credentials, so a line per rejection would be a log lever.
func TestRegisterLoggerWsRoute_UpgradeFailuresAreBudgeted(t *testing.T) {
	counter := commonmock.NewLogLineCounter("/log websocket upgrade failed")
	require.NoError(t, logger.AddLogObserver(counter, counter))
	t.Cleanup(func() { _ = logger.RemoveLogObserver(counter) })

	addr := startLogRouteServer(t, 0, 0)
	for i := 0; i < 20; i++ {
		_, resp, err := dialLogRouteWithOrigin(addr, "https://evil.example.com")
		require.Error(t, err)
		require.NotNil(t, resp)
		require.Equal(t, http.StatusForbidden, resp.StatusCode)
	}

	require.Eventually(t, func() bool { return counter.Count() >= 1 }, 2*time.Second, 10*time.Millisecond,
		"the first rejection must be logged")
	time.Sleep(50 * time.Millisecond)
	assert.Equal(t, 1, counter.Count(), "twenty rejections inside one window must fold into one line")
}
