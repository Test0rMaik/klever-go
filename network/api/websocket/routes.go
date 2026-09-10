package websocket

import (
	"fmt"
	"net"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	gorilla "github.com/gorilla/websocket"
	logger "github.com/klever-io/klever-go-logger"
	indexer "github.com/klever-io/klever-go/indexer"
	"github.com/klever-io/klever-go/websocket"
)

var log = logger.GetOrCreate("subscribe")

const (
	subscribeReadTimeout = 30 * time.Second
	subscribeOp          = "ws.Subscribe"
)

var upgrader = gorilla.Upgrader{
	// Origin isn't enforced here by design: the node runs headless behind an operator proxy
	// that owns origin/CORS policy, and /subscribe carries no ambient credentials (KLC-2450).
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type subscribeRequest struct {
	Addresses []string `json:"addresses"`
	Types     []string `json:"subscribed_types"`
}

// SubscribeOptions configures the hardening applied to the /subscribe route.
type SubscribeOptions struct {
	// MaxConnections caps simultaneous live connections node-wide (0 = unlimited).
	MaxConnections uint32
	// MaxConnectionsPerIP caps simultaneous live connections per source IP (0 = unlimited).
	MaxConnectionsPerIP uint32
	// AuthHandlers run before the WebSocket upgrade when /subscribe is `secured`.
	AuthHandlers []gin.HandlerFunc
}

func SubscribeTopics(ws *gin.Engine, hub *websocket.SocketHub, opts ...SubscribeOptions) {
	var opt SubscribeOptions
	if len(opts) > 0 {
		opt = opts[0]
	}

	// One limiter shared by every connection for the server lifetime.
	limiter := newConnLimiter(opt.MaxConnections, opt.MaxConnectionsPerIP)

	handlers := append([]gin.HandlerFunc{}, opt.AuthHandlers...)
	handlers = append(handlers, func(c *gin.Context) {
		handleSubscribe(c, hub, limiter)
	})
	ws.GET("/subscribe", handlers...)
}

func handleSubscribe(c *gin.Context, hub *websocket.SocketHub, limiter *connLimiter) {
	release, ok := limiter.acquire(remoteIP(c.Request))
	if !ok {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, map[string]string{"error": "too many websocket connections"})
		return
	}

	conn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		release()
		// A plain GET with no upgrade headers lands here, so this is one line per request
		// for anyone who asks. Rate-limited, on its own budget so a flood of these cannot
		// hide a rarer failure elsewhere.
		hub.LogUpgradeFailure(subscribeOp, err)
		return
	}

	go processSubscription(conn, hub, release)
}

// rejectHandshake answers a bad subscribe with a reason and closes. The write is bounded
// like loopOut's: without a deadline a peer that provokes a rejection and then stops
// reading could park this goroutine — and the limiter slot it holds — for as long as the
// TCP stack keeps trying. One small frame on a fresh connection lands in the kernel buffer
// and returns in practice, so this is the guard rail rather than a live hole, but every
// write to a peer-controlled socket should carry one.
func rejectHandshake(conn *gorilla.Conn, reason string) {
	_ = conn.SetWriteDeadline(time.Now().Add(subscribeReadTimeout))
	_ = conn.WriteJSON(map[string]string{"error": reason})
	_ = conn.Close()
}

// remoteIP returns the connecting peer's IP from the raw remote address. It does not
// trust X-Forwarded-For, so the per-IP connection cap cannot be spoofed (matches the
// existing sourceThrottler middleware).
func remoteIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func processSubscription(conn *gorilla.Conn, hub *websocket.SocketHub, release func()) {
	// Held for the whole connection lifetime: fires on every early return below and, once
	// the client is live, after <-client.Done() unblocks at teardown.
	defer release()

	// Bound the handshake frame before it is read.
	conn.SetReadLimit(hub.MaxMessageSize())

	if err := conn.SetReadDeadline(time.Now().Add(subscribeReadTimeout)); err != nil {
		hub.LogHandshakeFailure(subscribeOp, err)
		_ = conn.Close()
		return
	}

	var req subscribeRequest
	if err := conn.ReadJSON(&req); err != nil {
		// Malformed JSON, a wrong-typed field or an early close all arrive here, before any
		// of the caps below can apply — so this is the cheapest log lever on the route.
		hub.LogHandshakeFailure(subscribeOp, err)
		_ = conn.Close()
		return
	}
	// Deadline intentionally left armed; loopIn re-arms a lifetime deadline immediately.

	if len(req.Types) == 0 {
		rejectHandshake(conn, "subscribed_types must not be empty")
		return
	}

	parsedTypes, err := parseEventTypes(req.Types)
	if err != nil {
		rejectHandshake(conn, err.Error())
		return
	}

	if len(req.Addresses) > hub.MaxAddressesPerSubscribe() {
		rejectHandshake(conn, "too many addresses in a single subscribe")
		return
	}

	// Apply the hub's own input caps here, while this goroutine still owns the raw
	// connection and can answer with a reason. Past NewClient the writer goroutine owns the
	// socket, so a rejection can only be an abrupt close the peer cannot diagnose — and the
	// per-address byte cap is reachable on any fresh connection, not a theoretical branch.
	if err := hub.ValidateSubscription(parsedTypes, req.Addresses); err != nil {
		rejectHandshake(conn, err.Error())
		return
	}

	log.Debug(subscribeOp, "types", fmt.Sprintf("%v", parsedTypes), "addressCount", len(req.Addresses))
	client := websocket.NewClient(conn, hub)
	if err := hub.HandleClientInsertion(parsedTypes, req.Addresses, client); err != nil {
		// Pre-validated above, so what reaches here is a teardown race or the per-connection
		// cap. The hub picks the level and rate-limits the Warn, so this cannot become a
		// log-amplification lever.
		hub.LogRejectedInsertion(subscribeOp, err)
		// Close through the client: NewClient already started loopIn/loopOut, and closing
		// the raw conn under them logs two warnings per rejected connection.
		client.Close()
		return
	}

	// Block until the connection is torn down so release() fires exactly at teardown.
	<-client.Done()
}

func parseEventTypes(types []string) ([]indexer.EventType, error) {
	var parsed []indexer.EventType
	seen := make(map[indexer.EventType]struct{})

	for _, evType := range types {
		et, err := indexer.NewEventTypeStrict(evType)
		if err != nil {
			return nil, fmt.Errorf("invalid subscription type: %s", evType)
		}
		if _, ok := seen[et]; ok {
			continue
		}
		seen[et] = struct{}{}
		parsed = append(parsed, et)
	}

	return parsed, nil
}
