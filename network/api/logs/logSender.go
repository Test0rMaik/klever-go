package logs

import (
	"bytes"
	"errors"
	"net"
	"sync"
	"syscall"
	"time"

	"github.com/gorilla/websocket"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/api/shared"
	"github.com/klever-io/klever-go/tools/check"
	"github.com/klever-io/klever-go/tools/marshal"
	clientSocket "github.com/klever-io/klever-go/websocket"
)

const disconnectMessage = -1

const (
	maxHandshakeFrameSize = 4096
	handshakeTimeout      = 10 * time.Second

	// Keepalive timings, mirroring /subscribe (websocket/config.go). /log is a one-way
	// stream — the client says nothing after the handshake — so the pong is the only thing
	// that refreshes the read deadline. An honest tailer stays up because it answers our
	// pings; a wedged or vanished one is reclaimed at pongWait and gives back the
	// connection slot the /log cap exists to protect.
	pingPeriod = 15 * time.Second
	pongWait   = 30 * time.Second
	writeWait  = 10 * time.Second
)

// The logger profile is process-global, so the snapshot taken to revert it on disconnect must be
// too. A per-connection snapshot breaks as soon as two authenticated sessions overlap: the second
// captures the first's raised verbosity and, on disconnect, "reverts" the node to that instead of
// to the operator's original setting. Moving streaming off the handler goroutine made overlapping
// /log sessions the expected case rather than an edge one. Refcount instead: snapshot when the
// first session arrives, restore only when the last one leaves.
var (
	mutProfile      sync.Mutex
	profileRefs     int
	originalProfile logger.Profile
)

func acquireProfile() {
	mutProfile.Lock()
	defer mutProfile.Unlock()

	if profileRefs == 0 {
		originalProfile = logger.GetCurrentProfile()
	}
	profileRefs++
}

func releaseProfile(log logger.Logger) {
	mutProfile.Lock()
	defer mutProfile.Unlock()

	profileRefs--
	if profileRefs > 0 {
		return
	}
	profileRefs = 0

	// This is the single last-one-out restore; nothing retries it. A swallowed failure would
	// leave the node at the last session's verbosity — process-wide, disk sinks included —
	// while the log claimed otherwise.
	if err := originalProfile.Apply(); err != nil {
		log.Error("failed to revert log profile", "profile", originalProfile.String(), "error", err.Error())
		return
	}
	logger.NotifyProfileChange()
	log.Info("reverted log profile", "profile", originalProfile.String())
}

type logSender struct {
	marshalizer       marshal.Marshalizer
	conn              wsConn
	writer            *logWriter
	log               logger.Logger
	allowProfileApply bool
	// Per-connection copies of the keepalive constants, fixed at construction. They exist as
	// fields rather than direct constant reads so a test can shorten them for one sender and
	// observe a reclaim in milliseconds; nothing outside tests ever changes them, and there is
	// deliberately no operator-facing knob the way /subscribe has one.
	pingPeriod time.Duration
	pongWait   time.Duration
	writeWait  time.Duration
	// loops counts the ping and monitor goroutines so teardown can join them: a returned
	// StartSendingBlocking must mean a connection with no goroutine left behind, since the
	// caller releases the connection slot on that return.
	loops sync.WaitGroup
	// handshakeFailWarn budgets the failed-handshake line. Process-wide by default, since the
	// event is the same whichever route served it; a test gives a sender its own.
	handshakeFailWarn *clientSocket.DropWarner
}

// defaultHandshakeFailWarn is the one budget every sender in the process shares: a failed
// handshake is peer-driven — an authenticated client can send a malformed first frame at
// will — so a line per failure would hand it the log volume, exactly what /subscribe's
// equivalent already guards against.
var defaultHandshakeFailWarn = clientSocket.NewDropWarner(clientSocket.PeerDrivenLogWindow)

// NewLogSender returns a new component that is able to communicate with the log viewer application.
// After the correct handshake it will send all logs that come through the logger subsystem.
//
// allowProfileApply controls whether a client-supplied logger Profile (the first frame) may mutate
// the process-global logger. It must be true ONLY for authenticated connections: /log can be served
// unauthenticated, and letting an anonymous client apply a profile let a remote peer mute (*:NONE)
// or flood (*:TRACE) node logging process-wide (GHSA-9v8p-frvj-2pcm / KLC-2438). On a secured /log
// the operator is authenticated, so profile control (e.g. bumping verbosity to debug a live node)
// is restored as a trusted action and reverted on disconnect.
func NewLogSender(marshalizer marshal.Marshalizer, conn wsConn, log logger.Logger, allowProfileApply bool) (*logSender, error) {
	if check.IfNil(marshalizer) {
		return nil, ErrNilMarshalizer
	}
	if check.IfNil(log) {
		return nil, ErrNilLogger
	}
	if conn == nil {
		return nil, ErrNilWsConn
	}

	ls := &logSender{
		marshalizer:       marshalizer,
		log:               log,
		conn:              conn,
		allowProfileApply: allowProfileApply,
		pingPeriod:        pingPeriod,
		pongWait:          pongWait,
		writeWait:         writeWait,
		handshakeFailWarn: defaultHandshakeFailWarn,
	}

	return ls, nil
}

// registerLogWriter attaches this connection's writer to the process-global log subsystem.
// It runs after the handshake, never before: an observer registered in the constructor would
// have every log line formatted and fanned out for a client that never completed — or never
// intended to complete — the handshake.
func (ls *logSender) registerLogWriter() error {
	if ls.writer == nil {
		ls.writer = NewLogWriter()
	}

	formatter, err := logger.NewLogLineWrapperFormatter(ls.marshalizer)
	if err != nil {
		return err
	}

	return logger.AddLogObserver(ls.writer, formatter)
}

// StartSendingBlocking initializes the handshake by waiting for the first frame and after that
// will start sending logs information while monitoring the current connection. When profile
// application is allowed (authenticated client), a profile applied during the handshake is
// reverted once the client disconnects.
func (ls *logSender) StartSendingBlocking() {
	if ls.allowProfileApply {
		acquireProfile()
	}

	done := make(chan struct{})
	defer func() {
		close(done)
		_ = ls.conn.Close()
		// Closing the socket is what makes both loops return: the monitor's read fails and
		// the ping loop sees done. Joining them here makes a returned StartSendingBlocking,
		// and so the caller's release(), a connection with no goroutine still alive.
		ls.loops.Wait()
		if ls.writer != nil {
			_ = ls.writer.Close()
			_ = logger.RemoveLogObserver(ls.writer)
		}

		if ls.allowProfileApply {
			releaseProfile(ls.log)
		}
	}()

	err := ls.waitForProfile()
	if err != nil {
		// Budgeted: one line per window with the count folded in, since the peer decides how
		// often this happens. Quoted: the error carries client bytes on two paths — a gorilla
		// *CloseError echoes the peer's close reason, and a profile that fails to apply
		// echoes the level segment it could not parse.
		if count, ok := ls.handshakeFailWarn.Fire(); ok {
			ls.log.Warn("/log handshake failed", "error", shared.QuoteForLog(err.Error()), "similarSinceLastLog", count)
		}
		return
	}

	err = ls.registerLogWriter()
	if err != nil {
		ls.log.Error("/log cannot register log writer", "error", shared.QuoteForLog(err.Error()))
		return
	}

	// The caller's recover only contains StartSendingBlocking itself, not the goroutines
	// spawned here. Both are joined in the defer above, and Done runs outside SafeRun so
	// the join also covers a panicked loop's recovery — the log line and the close — not
	// only the loop body.
	ls.loops.Add(2)
	go func() {
		defer ls.loops.Done()
		shared.SafeRun(ls.log, "/log pingLoop", ls.conn, func() { ls.pingLoop(done) })
	}()
	go func() {
		defer ls.loops.Done()
		shared.SafeRun(ls.log, "/log monitorConnection", ls.conn, ls.monitorConnection)
	}()
	ls.doSendContinuously()
}

// pingLoop keeps the streaming-phase read deadline refreshed. Because /log clients never send
// data frames, the pong is the only thing that re-arms it: without these pings the deadline
// would kill every honest tailer at pongWait. WriteControl is safe to call concurrently with
// the other writer; sendMessage stays the only regular writer.
func (ls *logSender) pingLoop(done <-chan struct{}) {
	ticker := time.NewTicker(ls.pingPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-done:
			return
		case <-ticker.C:
			err := ls.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(ls.writeWait))
			if err != nil {
				// Close so the slot is reclaimed now rather than at the read deadline.
				_ = ls.conn.Close()
				return
			}
		}
	}
}

// waitForProfile performs the /log handshake: it reads the first client frame. A client-provided
// profile is applied to the process-global logger ONLY when allowProfileApply is set (authenticated
// connection). On an unauthenticated /log the profile is parsed (to reject malformed handshakes) but
// never applied, so a remote peer cannot mute (*:NONE) or flood (*:TRACE) node logging process-wide
// (GHSA-9v8p-frvj-2pcm / KLC-2438).
func (ls *logSender) waitForProfile() error {
	ls.conn.SetReadLimit(maxHandshakeFrameSize)

	err := ls.conn.SetReadDeadline(time.Now().Add(handshakeTimeout))
	if err != nil {
		return err
	}

	_, message, err := ls.conn.ReadMessage()
	if err != nil {
		return err
	}

	// Hand the connection over to the keepalive: the handshake deadline is replaced by a
	// rolling pongWait deadline that pingLoop's pongs and monitorConnection refresh.
	// Clearing it outright would leave a peer that stops reading pinning its connection
	// slot for the life of the process.
	ls.conn.SetPongHandler(func(string) error {
		return ls.conn.SetReadDeadline(time.Now().Add(ls.pongWait))
	})

	err = ls.conn.SetReadDeadline(time.Now().Add(ls.pongWait))
	if err != nil {
		return err
	}

	if bytes.Equal(message, []byte(core.DefaultLogProfileIdentifier)) {
		return nil
	}

	profile, err := logger.UnmarshalProfile(message)
	if err != nil {
		return err
	}

	if !ls.allowProfileApply {
		ls.log.Info("ignoring client-provided log profile on unauthenticated /log", "profile", shared.QuoteForLog(profile.String()))
		return nil
	}

	ls.log.Info("websocket log profile received", "profile", shared.QuoteForLog(profile.String()))
	if err := profile.Apply(); err != nil {
		return err
	}

	logger.NotifyProfileChange()
	return nil
}

func (ls *logSender) monitorConnection() {
	var err error
	var mt int

	// Close the socket first, then the writer. Closing only the writer would let the sender
	// drain whatever is already queued — up to msgQueueSize messages, each under a fresh
	// write deadline — to a peer that has just failed its read deadline, holding the
	// connection slot for minutes past the point it was declared dead. With the socket
	// closed the next write fails at once; closing the writer is what then unblocks
	// doSendContinuously and lets StartSendingBlocking return. No nil check on the writer:
	// this goroutine starts only after registerLogWriter succeeded.
	defer func() {
		_ = ls.conn.Close()
		_ = ls.writer.Close()
	}()

	for {
		mt, _, err = ls.conn.ReadMessage()
		ls.log.Trace("message type", "value", mt)
		if mt == websocket.CloseMessage || mt == disconnectMessage {
			return
		}
		if err != nil {
			return
		}
		// A client that is talking to us is alive; refresh alongside the pong handler.
		if err = ls.conn.SetReadDeadline(time.Now().Add(ls.pongWait)); err != nil {
			return
		}
	}
}

func (ls *logSender) doSendContinuously() {
	for {
		shouldStop := ls.sendMessage()
		if shouldStop {
			return
		}
	}
}

func (ls *logSender) sendMessage() (shouldStop bool) {
	data, ok := ls.writer.ReadBlocking()
	if !ok {
		return true
	}

	// Bound the write. A peer that answers pings but never drains its socket would otherwise
	// park this goroutine on a full kernel send buffer forever: StartSendingBlocking never
	// returns, so its deferred release() never runs and the connection slot is gone for good.
	err := ls.conn.SetWriteDeadline(time.Now().Add(ls.writeWait))
	if err != nil {
		ls.log.Error("/log write deadline could not be armed", "error", shared.QuoteForLog(err.Error()))
		return true
	}

	err = ls.conn.WriteMessage(websocket.TextMessage, data)
	if err != nil {
		if isTeardownError(err) {
			ls.log.Info("/log connection closed", "reason", shared.QuoteForLog(err.Error()))
		} else {
			ls.log.Error("/log write failed", "error", shared.QuoteForLog(err.Error()))
		}

		return true
	}

	return false
}

// isTeardownError reports whether a write failed because the connection is going away rather
// than because something is wrong: our own Close (the monitor's after a failed read, pingLoop's
// after a failed ping), a close frame already sent, the peer gone (EPIPE, ECONNRESET), or the
// write deadline expiring on a peer that stopped draining — which is the reclaim working.
// Anything else is logged as a fault. None of these carry peer bytes; the text is quoted
// anyway, for the same reason every other error on this path is.
func isTeardownError(err error) bool {
	if errors.Is(err, net.ErrClosed) || errors.Is(err, websocket.ErrCloseSent) ||
		errors.Is(err, syscall.EPIPE) || errors.Is(err, syscall.ECONNRESET) {
		return true
	}

	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}
