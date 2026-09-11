package logs_test

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	logger "github.com/klever-io/klever-go-logger"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/api/logs"
	"github.com/klever-io/klever-go/network/api/shared"
	clientSocket "github.com/klever-io/klever-go/websocket"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func removeWriterFromLogSubsystem(w io.Writer) {
	_ = logger.RemoveLogObserver(w)
}

func createMockLogSender() (*logs.LogSender, *mock.WsConnStub, io.Writer) {
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error {
		return nil
	})
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		profile := logger.Profile{LogLevelPatterns: "*:INFO"}
		profileJson, _ := profile.Marshal()
		return websocket.TextMessage, profileJson, nil
	})

	ls, _ := logs.NewLogSender(
		&mock.MarshalizerStub{},
		conn,
		&mock.LoggerStub{},
		false,
	)
	removeWriterFromLogSubsystem(ls.Writer())
	ls.SetWriter(logs.NewLogWriter())

	lsender := &logs.LogSender{}
	lsender.Set(ls)
	return lsender, conn, ls.Writer()
}

//------- NewLogSender

func TestNewLogSender_NilMarshalizerShouldErr(t *testing.T) {
	t.Parallel()

	ls, err := logs.NewLogSender(nil, &mock.WsConnStub{}, &mock.LoggerStub{}, false)

	assert.Nil(t, ls)
	assert.Equal(t, logs.ErrNilMarshalizer, err)
}

func TestNewLogSender_NilConnectionShouldErr(t *testing.T) {
	t.Parallel()

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, nil, &mock.LoggerStub{}, false)

	assert.Nil(t, ls)
	assert.Equal(t, logs.ErrNilWsConn, err)
}

func TestNewLogSender_NilLoggerShouldErr(t *testing.T) {
	t.Parallel()

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, &mock.WsConnStub{}, nil, false)

	assert.Nil(t, ls)
	assert.Equal(t, logs.ErrNilLogger, err)
}

func TestNewLogSender_ShouldWork(t *testing.T) {
	t.Parallel()

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, &mock.WsConnStub{}, &mock.LoggerStub{}, false)

	assert.NotNil(t, ls)
	assert.Nil(t, err)
	assert.Nil(t, ls.Writer())
}

//------- StartSendingBlocking

func TestLogSender_StartSendingBlockingConnReadMessageErrShouldCloseConn(t *testing.T) {
	t.Parallel()

	closeCalled := false
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error {
		closeCalled = true
		return nil
	})
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		return websocket.TextMessage, nil, errors.New("")
	})
	ls, _ := logs.NewLogSender(
		&mock.MarshalizerStub{},
		conn,
		&mock.LoggerStub{},
		false,
	)
	removeWriterFromLogSubsystem(ls.Writer())

	ls.StartSendingBlocking()

	assert.True(t, closeCalled)
}

func TestLogSender_StartSendingBlockingWrongPatternShouldCloseConn(t *testing.T) {
	t.Parallel()

	closeCalled := false
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error {
		closeCalled = true
		return nil
	})
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		return websocket.TextMessage, []byte("wrong log pattern"), nil
	})
	ls, _ := logs.NewLogSender(
		&mock.MarshalizerStub{},
		conn,
		&mock.LoggerStub{},
		false,
	)
	removeWriterFromLogSubsystem(ls.Writer())

	ls.StartSendingBlocking()

	assert.True(t, closeCalled)
}

func TestLogSender_StartSendingBlockingSendsMessage(t *testing.T) {
	t.Parallel()

	ls, conn, writer := createMockLogSender()
	data := []byte("random data")
	// The sender is a registered log observer now, so anything the process logs meanwhile
	// arrives on the same stream: assert our payload was sent, not that it was sent alone.
	var written [][]byte
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error {
		written = append(written, data)
		return nil
	})

	go func() {
		//watchdog function
		time.Sleep(time.Millisecond * 10)

		_ = ls.Writer().Close()
	}()

	_, err := writer.Write(data)
	ls.StartSendingBlocking()

	assert.Nil(t, err)
	assert.Contains(t, written, data)
}

func TestLogSender_StartSendingBlockingSendsMessageAndStopsWhenReadClose(t *testing.T) {
	t.Parallel()

	ls, conn, writer := createMockLogSender()
	data := []byte("random data")
	var written [][]byte
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error {
		written = append(written, data)
		return nil
	})

	go func() {
		//watchdog function
		time.Sleep(time.Millisecond * 10)

		conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
			return websocket.CloseMessage, []byte(""), nil
		})
	}()

	_, err := writer.Write(data)
	ls.StartSendingBlocking()

	assert.Nil(t, err)
	assert.Contains(t, written, data)
}

// TestLogSender_PanicInSpawnedGoroutineDoesNotCrashTheNode covers shared.SafeGo. Since streaming moved
// off the request goroutine, neither gin.Recovery() nor the caller's recover reaches the two
// goroutines StartSendingBlocking spawns, so a panic in either used to take the whole node down
// rather than the one connection. Without the recover this test kills the test binary; with it,
// the connection tears down and the sender returns.
func TestLogSender_PanicInSpawnedGoroutineDoesNotCrashTheNode(t *testing.T) {
	t.Parallel()

	readCount := 0
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			// Handshake frame; the next read is monitorConnection's.
			return websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier), nil
		}
		panic("boom inside monitorConnection")
	})

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, false)
	require.NoError(t, err)

	returned := make(chan struct{})
	go func() {
		ls.StartSendingBlocking()
		close(returned)
	}()

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("a panic in a spawned goroutine must be recovered and tear the connection down")
	}
}

// TestLogSender_HandshakeErrorIsEscapedBeforeLogging is the CWE-117 regression for the handshake
// failure path. The error text itself is client-controlled: gorilla puts the peer's close reason
// verbatim into *CloseError.Error(), and the logger formatters do not escape newlines, so logging
// it raw lets a peer that never completes the handshake forge a whole ERROR line into the node's
// log file and into every other /log observer. The guard belongs at the log call, not on the
// profile string alone.
func TestLogSender_HandshakeErrorIsEscapedBeforeLogging(t *testing.T) {
	t.Parallel()

	forged := "bye\nERROR [consensus] forged entry"

	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		return 0, nil, &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: forged}
	})

	var mutLogged sync.Mutex
	logged := make([]string, 0)
	log := &mock.LoggerStub{
		LogCalled: func(level logger.LogLevel, message string, args ...interface{}) {
			if message != "/log handshake failed" {
				return
			}

			mutLogged.Lock()
			defer mutLogged.Unlock()
			for _, arg := range args {
				if text, ok := arg.(string); ok {
					logged = append(logged, text)
				}
			}
		},
	}

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, log, false)
	require.NoError(t, err)
	ls.SetHandshakeFailBudget(clientSocket.NewDropWarner(time.Hour))
	t.Cleanup(func() { removeWriterFromLogSubsystem(ls.Writer()) })

	ls.StartSendingBlocking()

	mutLogged.Lock()
	defer mutLogged.Unlock()

	carriesReason := false
	for _, text := range logged {
		if !strings.Contains(text, "forged entry") {
			continue
		}
		carriesReason = true
		assert.NotContains(t, text, "\n",
			"a close reason must not reach the log with its newline intact")
		assert.Contains(t, text, `\n`, "the newline must survive as an escape sequence")
	}
	assert.True(t, carriesReason, "the handshake error must be logged, escaped, not swallowed")
}

// TestLogSender_UnauthenticatedClientProfileIsIgnored is the anti-PoC regression for
// GHSA-9v8p-frvj-2pcm / KLC-2438: on an unauthenticated /log (allowProfileApply=false) a profile
// sent by a client as the first frame must NOT mutate the process-global logger.
// Not parallel: it asserts on global logger state.
func TestLogSender_UnauthenticatedClientProfileIsIgnored(t *testing.T) {
	err := logger.SetLogLevel("*:INFO")
	assert.Nil(t, err)
	baseline := logger.GetLogLevelPattern()
	assert.NotEqual(t, "*:NONE", baseline)
	t.Cleanup(func() { _ = logger.SetLogLevel("*:INFO") })

	mutePayload := []byte(`{"LogLevelPatterns":"*:NONE","WithCorrelation":false,"WithLoggerName":false}`)

	readCount := 0
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			// Attacker handshake frame: a global mute profile.
			return websocket.TextMessage, mutePayload, nil
		}
		// End the stream: monitorConnection returns on CloseMessage and closes the
		// writer, which unblocks doSendContinuously so StartSendingBlocking returns.
		return websocket.CloseMessage, nil, nil
	})

	// allowProfileApply=false => unauthenticated connection.
	ls, _ := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, false)

	ls.StartSendingBlocking()

	assert.Equal(t, baseline, logger.GetLogLevelPattern(), "unauthenticated client profile must not change the global log level")
	assert.NotEqual(t, "*:NONE", logger.GetLogLevelPattern())
}

// TestLogSender_AuthenticatedClientProfileIsAppliedAndReverted verifies that on a secured /log
// (allowProfileApply=true) an authenticated operator CAN apply a logger profile while connected
// — the useful debugging capability — and that it is reverted to the prior profile on disconnect.
// Not parallel: it asserts on global logger state.
func TestLogSender_AuthenticatedClientProfileIsAppliedAndReverted(t *testing.T) {
	err := logger.SetLogLevel("*:INFO")
	assert.Nil(t, err)
	baseline := logger.GetLogLevelPattern()
	assert.NotEqual(t, "*:NONE", baseline)
	t.Cleanup(func() { _ = logger.SetLogLevel("*:INFO") })

	mutePayload := []byte(`{"LogLevelPatterns":"*:NONE","WithCorrelation":false,"WithLoggerName":false}`)

	var midLevel string
	readCount := 0
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			return websocket.TextMessage, mutePayload, nil
		}
		// This read happens in monitorConnection, after waitForProfile applied the profile:
		// capture the live (mid-connection) global level before ending the stream.
		if midLevel == "" {
			midLevel = logger.GetLogLevelPattern()
		}
		return websocket.CloseMessage, nil, nil
	})

	// allowProfileApply=true => authenticated connection.
	ls, _ := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, true)

	ls.StartSendingBlocking()

	assert.Equal(t, "*:NONE", midLevel, "authenticated client profile should be applied while connected")
	assert.Equal(t, baseline, logger.GetLogLevelPattern(), "profile should be reverted to baseline on disconnect")
}

func TestLogSender_StartSendingBlockingConnWriteFailsShouldStop(t *testing.T) {
	t.Parallel()

	ls, conn, writer := createMockLogSender()
	data := []byte("random data")
	closeCalled := false
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error {
		return errors.New("")
	})
	conn.SetCloseHandler(func() error {
		closeCalled = true
		return nil
	})

	_, _ = writer.Write(data)
	ls.StartSendingBlocking()

	assert.True(t, closeCalled)
}

// TestLogSender_StartSendingBlockingArmsKeepaliveAfterHandshake is the honest-tailer
// guarantee. The handshake deadline must be replaced, not cleared: cleared, a peer that stops
// reading pins its connection slot for the life of the process; left as the short handshake
// deadline, a legitimate log tailer is killed 10 seconds in.
//
// It builds its own stub rather than using createMockLogSender's never-failing read handler.
// With that one, monitorConnection keeps looping and its own refresh at the top of the read
// loop fills deadlines[1], so the assertion passed whether or not waitForProfile armed
// anything — deleting the handover left this test green. Ending the stream on frame 2 makes
// monitorConnection return before it can refresh, so the history is exactly the two deadlines
// waitForProfile armed and the handover can be asserted by value.
func TestLogSender_StartSendingBlockingArmsKeepaliveAfterHandshake(t *testing.T) {
	t.Parallel()

	readCount := 0
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error { return nil })
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			profile := logger.Profile{LogLevelPatterns: "*:INFO"}
			profileJson, _ := profile.Marshal()
			return websocket.TextMessage, profileJson, nil
		}
		// End the stream so monitorConnection returns without refreshing the deadline.
		return websocket.CloseMessage, nil, nil
	})

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, false)
	require.NoError(t, err)
	t.Cleanup(func() { removeWriterFromLogSubsystem(ls.Writer()) })

	start := time.Now()
	ls.StartSendingBlocking()

	// The literals are the shipped handshakeTimeout and pongWait; both are unexported.
	deadlines := conn.ReadDeadlines()
	require.Len(t, deadlines, 2, "waitForProfile must arm the handshake deadline and then replace it")
	assert.WithinDuration(t, start.Add(10*time.Second), deadlines[0], 2*time.Second,
		"first deadline must be the short handshake one")
	assert.WithinDuration(t, start.Add(30*time.Second), deadlines[1], 2*time.Second,
		"handshake deadline must hand over to a pongWait-sized one, not be cleared or left short")

	assert.Equal(t, int64(4096), conn.ReadLimit())

	// The pong is what keeps an idle-but-live tailer alive; without a handler it could not.
	before := conn.LastReadDeadline()
	require.NoError(t, conn.Pong())
	after := conn.LastReadDeadline()
	// Strictly after, not merely "not before": `before` is already the non-zero pongWait
	// deadline waitForProfile armed, so a deleted SetPongHandler leaves `after == before` and
	// both an IsZero check and a !Before check still pass. Pong() itself now errors when no
	// handler is registered, so the require above fails first on that mutation.
	assert.True(t, after.After(before), "pong must push the read deadline forward")
}

// TestLogSender_SendMessageArmsWriteDeadline covers the other half of the reclaim: a peer that
// answers pings but never drains its socket would park WriteMessage on a full kernel send
// buffer forever, so StartSendingBlocking would never return and its slot would never be freed.
func TestLogSender_SendMessageArmsWriteDeadline(t *testing.T) {
	t.Parallel()

	ls, conn, writer := createMockLogSender()

	// Track ordering with local counters, not by querying the stub: WriteMessage invokes its
	// handler while holding the stub's mutex, so a getter call from inside would self-deadlock.
	deadlinesArmed, writes := 0, 0
	armedBeforeEveryWrite := true
	conn.SetWriteDeadlineHandler(func(time.Time) error {
		deadlinesArmed++
		return nil
	})
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error {
		writes++
		if deadlinesArmed < writes {
			armedBeforeEveryWrite = false
		}
		return nil
	})

	go func() {
		time.Sleep(time.Millisecond * 10)

		_ = ls.Writer().Close()
	}()

	_, _ = writer.Write([]byte("random data"))
	ls.StartSendingBlocking()

	assert.Positive(t, writes, "the test payload should have been written")
	assert.True(t, armedBeforeEveryWrite, "a write deadline must be armed before every WriteMessage")
	for _, deadline := range conn.WriteDeadlines() {
		assert.False(t, deadline.IsZero(), "write deadline must never be cleared")
	}
}

// TestLogSender_WriteDeadlineErrShouldStop makes sure a connection whose write deadline cannot
// be armed is torn down rather than written to unbounded.
func TestLogSender_WriteDeadlineErrShouldStop(t *testing.T) {
	t.Parallel()

	ls, conn, writer := createMockLogSender()

	writeMessageCalled := false
	closeCalled := false
	conn.SetWriteDeadlineHandler(func(time.Time) error {
		return errors.New("cannot arm write deadline")
	})
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error {
		writeMessageCalled = true
		return nil
	})
	conn.SetCloseHandler(func() error {
		closeCalled = true
		return nil
	})

	_, _ = writer.Write([]byte("random data"))
	ls.StartSendingBlocking()

	assert.False(t, writeMessageCalled, "no unbounded write may happen once the deadline failed")
	assert.True(t, closeCalled)
}

// TestLogSender_StartSendingBlockingHandshakeDeadlineErrShouldCloseConnAndNotStream covers both
// SetReadDeadline calls around the handshake: the one that arms it before the first read, and
// the one that hands the connection over to the keepalive after it. Either failing must close
// the connection and stream nothing.
func TestLogSender_StartSendingBlockingHandshakeDeadlineErrShouldCloseConnAndNotStream(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		failOnCall  int
		expectsRead bool
	}{
		{name: "arming the handshake deadline fails", failOnCall: 1, expectsRead: false},
		{name: "arming the keepalive deadline fails", failOnCall: 2, expectsRead: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			expectedErr := errors.New("read deadline failure")
			closeCalled := false
			readMessageCalled := false
			writeMessageCalled := false
			loggedErr := ""
			numDeadlineCalls := 0

			conn := &mock.WsConnStub{}
			conn.SetCloseHandler(func() error {
				closeCalled = true
				return nil
			})
			conn.SetReadDeadlineHandler(func(time.Time) error {
				numDeadlineCalls++
				if numDeadlineCalls == tt.failOnCall {
					return expectedErr
				}
				return nil
			})
			conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
				readMessageCalled = true
				profile := logger.Profile{LogLevelPatterns: "*:INFO"}
				profileJson, _ := profile.Marshal()
				return websocket.TextMessage, profileJson, nil
			})
			conn.SetWriteMessageHandler(func(messageType int, data []byte) error {
				writeMessageCalled = true
				return nil
			})

			log := &mock.LoggerStub{
				LogCalled: func(level logger.LogLevel, message string, args ...interface{}) {
					if message != "/log handshake failed" {
						return
					}
					// The error travels as a quoted "error" field now, not as the message
					// itself, so a client-controlled close reason cannot forge a log line.
					for i := 0; i+1 < len(args); i += 2 {
						if args[i] == "error" {
							loggedErr, _ = args[i+1].(string)
						}
					}
				},
			}

			ls, _ := logs.NewLogSender(&mock.MarshalizerStub{}, conn, log, false)
			ls.SetHandshakeFailBudget(clientSocket.NewDropWarner(time.Hour))

			done := make(chan struct{})
			go func() {
				ls.StartSendingBlocking()
				close(done)
			}()

			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("StartSendingBlocking did not return after the read deadline could not be set")
			}

			assert.Equal(t, tt.failOnCall, numDeadlineCalls)
			assert.Equal(t, tt.expectsRead, readMessageCalled)
			assert.True(t, closeCalled)
			assert.False(t, writeMessageCalled)
			assert.Equal(t, shared.QuoteForLog(expectedErr.Error()), loggedErr)
			assert.Nil(t, ls.Writer(), "no observer may be registered when the handshake failed")
		})
	}
}

// TestLogSender_ObserverLifecycle documents the registration change this PR makes: the
// process-global log observer is attached only once the handshake succeeds — so a client that
// never completes one costs nothing — and detached again at teardown.
func TestLogSender_ObserverLifecycle(t *testing.T) {
	t.Parallel()

	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetWriteMessageHandler(func(messageType int, data []byte) error { return nil })

	readCount := 0
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			return websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier), nil
		}
		return websocket.CloseMessage, nil, nil
	})

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, false)
	require.NoError(t, err)
	require.Nil(t, ls.Writer(), "no observer before the handshake")

	// Registration is proven from inside the session, on the second read: removing the
	// observer succeeds only if the handshake registered it. It is put straight back so the
	// teardown below has something to remove. Checking "remove errors afterwards" alone
	// cannot tell registered-then-removed from never-registered — both error.
	formatter, err := logger.NewLogLineWrapperFormatter(&mock.MarshalizerStub{})
	require.NoError(t, err)
	registeredDuringSession := false
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			return websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier), nil
		}
		writer := ls.Writer()
		registeredDuringSession = writer != nil && logger.RemoveLogObserver(writer) == nil
		if registeredDuringSession {
			_ = logger.AddLogObserver(writer, formatter)
		}
		return websocket.CloseMessage, nil, nil
	})

	ls.StartSendingBlocking()

	writer := ls.Writer()
	require.NotNil(t, writer, "the handshake must allocate the writer")
	assert.True(t, registeredDuringSession, "the handshake must register the observer, not only allocate the writer")
	// It was registered while the session was live, so the only way this removal can fail
	// is that teardown already removed it.
	assert.Error(t, logger.RemoveLogObserver(writer), "observer must be removed at teardown")
}

// drainConn is a wsConn whose callbacks run under no lock, so a test can order the monitor's
// failing read against the sender's writes by construction rather than by sleeping. The second
// read fails only once the first write attempt has begun; that first attempt then parks until
// the read has failed and gives the close it must trigger a generous bound to land. Later
// attempts are immediate. On correct code the socket is closed by the time the first attempt
// resumes, so it fails and the count stays at one; on code that closes only the writer nothing
// ever closes the socket, and every queued message is attempted.
type drainConn struct {
	profile    []byte
	reads      atomic.Int32
	attempts   atomic.Int32
	firstWrite chan struct{}
	readFailed chan struct{}
	closed     chan struct{}
	writeOnce  sync.Once
	readOnce   sync.Once
	graceOnce  sync.Once
	closeOnce  sync.Once
}

func newDrainConn(profile []byte) *drainConn {
	return &drainConn{
		profile:    profile,
		firstWrite: make(chan struct{}),
		readFailed: make(chan struct{}),
		closed:     make(chan struct{}),
	}
}

func (c *drainConn) ReadMessage() (int, []byte, error) {
	if c.reads.Add(1) == 1 {
		return websocket.TextMessage, c.profile, nil
	}
	select {
	case <-c.firstWrite:
	case <-c.closed:
	}
	c.readOnce.Do(func() { close(c.readFailed) })
	return 0, nil, errors.New("read deadline exceeded")
}

func (c *drainConn) WriteMessage(int, []byte) error {
	c.attempts.Add(1)
	c.writeOnce.Do(func() { close(c.firstWrite) })
	<-c.readFailed
	c.graceOnce.Do(func() {
		select {
		case <-c.closed:
		case <-time.After(2 * time.Second):
		}
	})
	select {
	case <-c.closed:
		return net.ErrClosed
	default:
		return nil
	}
}

func (c *drainConn) Close() error {
	c.closeOnce.Do(func() { close(c.closed) })
	return nil
}

func (c *drainConn) WriteControl(int, []byte, time.Time) error { return nil }
func (c *drainConn) SetReadLimit(int64)                        {}
func (c *drainConn) SetReadDeadline(time.Time) error           { return nil }
func (c *drainConn) SetWriteDeadline(time.Time) error          { return nil }
func (c *drainConn) SetPongHandler(func(string) error)         {}

// TestLogSender_ReadFailureClosesTheSocketBeforeTheQueueDrains pins the reclaim on the read
// path. When the read deadline fires, closing only the writer lets the sender drain everything
// already queued — up to msgQueueSize messages, each under a fresh write deadline — to a peer
// that reads slowly and never pongs, so the slot outlives the deadline by minutes. The monitor
// must close the socket too, so the next write fails at once.
func TestLogSender_ReadFailureClosesTheSocketBeforeTheQueueDrains(t *testing.T) {
	t.Parallel()

	profile := logger.Profile{LogLevelPatterns: "*:INFO"}
	profileJSON, err := profile.Marshal()
	require.NoError(t, err)
	conn := newDrainConn(profileJSON)

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, false)
	require.NoError(t, err)
	ls.SetWriter(logs.NewLogWriter())
	t.Cleanup(func() { removeWriterFromLogSubsystem(ls.Writer()) })

	queued := logs.MsgQueueSize / 2
	for i := 0; i < queued; i++ {
		_, err := ls.Writer().Write([]byte("queued before the read failed"))
		require.NoError(t, err)
	}

	ls.StartSendingBlocking()

	assert.Equal(t, 1, int(conn.attempts.Load()),
		"the first write after the failed read must find the socket closed; draining the queue instead keeps the slot for writes*writeWait")
}

// TestLogSender_HandshakeFailBudgetIsSharedByDefault pins what the budget test above cannot: every
// production sender is handed the one process-wide budget, so a constructor that allocated a fresh
// one per sender — a line per connection again — fails here by pointer.
func TestLogSender_HandshakeFailBudgetIsSharedByDefault(t *testing.T) {
	t.Parallel()

	first, err := logs.NewLogSender(&mock.MarshalizerStub{}, &mock.WsConnStub{}, &mock.LoggerStub{}, false)
	require.NoError(t, err)
	second, err := logs.NewLogSender(&mock.MarshalizerStub{}, &mock.WsConnStub{}, &mock.LoggerStub{}, false)
	require.NoError(t, err)

	require.NotNil(t, first.HandshakeFailBudget())
	assert.Same(t, logs.DefaultHandshakeFailBudget(), first.HandshakeFailBudget())
	assert.Same(t, first.HandshakeFailBudget(), second.HandshakeFailBudget(),
		"two senders must share one budget, or a peer reconnecting gets a line per connection")
}

// TestLogSender_PanicRecoveryCompletesBeforeReturn pins the join: StartSendingBlocking must not
// return — and so the caller's release() must not run — while the recovery of a panicked loop
// is still logging and closing. The logger stub stalls on the panic line; a return during that
// stall is a goroutine outliving the release it was supposed to precede.
func TestLogSender_PanicRecoveryCompletesBeforeReturn(t *testing.T) {
	t.Parallel()

	readCount := 0
	conn := &mock.WsConnStub{}
	conn.SetCloseHandler(func() error { return nil })
	conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
		readCount++
		if readCount == 1 {
			return websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier), nil
		}
		panic("boom inside monitorConnection")
	})

	var recoveryLogged atomic.Bool
	log := &mock.LoggerStub{
		LogCalled: func(level logger.LogLevel, message string, args ...interface{}) {
			if message != "panic in detached websocket goroutine" {
				return
			}
			time.Sleep(100 * time.Millisecond)
			recoveryLogged.Store(true)
		},
	}

	ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, log, false)
	require.NoError(t, err)

	ls.StartSendingBlocking()

	assert.True(t, recoveryLogged.Load(),
		"StartSendingBlocking returned while the panicked loop's recovery was still running")
}

// TestLogSender_HandshakeFailuresAreBudgeted counts what the logger emits: twenty senders whose
// peer fails the handshake, one line. An authenticated client can fail the handshake at will,
// so a line per failure would be a log lever.
func TestLogSender_HandshakeFailuresAreBudgeted(t *testing.T) {
	t.Parallel()

	budget := clientSocket.NewDropWarner(time.Hour)
	lines := 0
	log := &mock.LoggerStub{
		LogCalled: func(level logger.LogLevel, message string, args ...interface{}) {
			if message == "/log handshake failed" {
				lines++
			}
		},
	}

	for i := 0; i < 20; i++ {
		conn := &mock.WsConnStub{}
		conn.SetCloseHandler(func() error { return nil })
		conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
			return 0, nil, &websocket.CloseError{Code: websocket.CloseNormalClosure, Text: "bye"}
		})

		ls, err := logs.NewLogSender(&mock.MarshalizerStub{}, conn, log, false)
		require.NoError(t, err)
		ls.SetHandshakeFailBudget(budget)
		ls.StartSendingBlocking()
	}

	assert.Equal(t, 1, lines, "twenty failed handshakes inside one window must fold into one line")
}

// TestIsTeardownError pins which write failures are the connection going away, logged as such,
// and which are faults. The peer gone and the write deadline expiring are the reclaim working,
// not something wrong.
func TestIsTeardownError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		err      error
		teardown bool
	}{
		{name: "our own close", err: net.ErrClosed, teardown: true},
		{name: "our own close, wrapped by net", err: &net.OpError{Op: "write", Err: net.ErrClosed}, teardown: true},
		{name: "close frame already sent", err: websocket.ErrCloseSent, teardown: true},
		{name: "close frame already sent, wrapped", err: fmt.Errorf("write: %w", websocket.ErrCloseSent), teardown: true},
		// The old check matched the text; the value is what gorilla returns, and an
		// unrelated error that happens to share the words must not be mistaken for it.
		{name: "same text, different error, is a fault", err: errors.New("websocket: close sent"), teardown: false},
		{name: "peer gone: EPIPE", err: &net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.EPIPE)}, teardown: true},
		{name: "peer gone: ECONNRESET", err: &net.OpError{Op: "write", Err: os.NewSyscallError("write", syscall.ECONNRESET)}, teardown: true},
		{name: "write deadline expired", err: &net.OpError{Op: "write", Err: os.ErrDeadlineExceeded}, teardown: true},
		{name: "anything else is a fault", err: errors.New("websocket: bad write"), teardown: false},
		{name: "nil", err: nil, teardown: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.teardown, logs.IsTeardownError(tt.err))
		})
	}
}

// TestLogSender_ProfileIsRevertedOnlyWhenTheLastSessionLeaves covers the refcount. Moving
// streaming off the request goroutine made overlapping authenticated /log sessions the expected
// case, and a per-connection snapshot breaks there: the second session captures the first's
// raised verbosity and, on disconnect, "reverts" the node to that instead of the operator's
// original setting. Not parallel: it asserts on global logger state.
func TestLogSender_ProfileIsRevertedOnlyWhenTheLastSessionLeaves(t *testing.T) {
	logs.ResetProfileRefs()
	require.NoError(t, logger.SetLogLevel("*:INFO"))
	baseline := logger.GetLogLevelPattern()
	t.Cleanup(func() { _ = logger.SetLogLevel("*:INFO") })

	// NewLogSender returns an unexported type, so name it by the one method used here.
	type session interface{ StartSendingBlocking() }

	// Each sender blocks in its handshake read until released, so both are live at once.
	newSession := func(profileJSON string) (session, chan struct{}) {
		release := make(chan struct{})
		readCount := 0

		conn := &mock.WsConnStub{}
		conn.SetCloseHandler(func() error { return nil })
		conn.SetWriteMessageHandler(func(messageType int, data []byte) error { return nil })
		conn.SetReadMessageHandler(func() (messageType int, p []byte, err error) {
			readCount++
			if readCount == 1 {
				return websocket.TextMessage, []byte(profileJSON), nil
			}
			// Park here to hold the session open, but never indefinitely: the stub invokes
			// handlers under its own mutex, so a permanent park would freeze the connection.
			select {
			case <-release:
			case <-time.After(10 * time.Second):
			}
			return websocket.CloseMessage, nil, nil
		})

		ls, lerr := logs.NewLogSender(&mock.MarshalizerStub{}, conn, &mock.LoggerStub{}, true)
		require.NoError(t, lerr)
		return ls, release
	}

	first, releaseFirst := newSession(`{"LogLevelPatterns":"*:DEBUG","WithCorrelation":false,"WithLoggerName":false}`)
	second, releaseSecond := newSession(`{"LogLevelPatterns":"*:TRACE","WithCorrelation":false,"WithLoggerName":false}`)

	firstDone := make(chan struct{})
	go func() {
		first.StartSendingBlocking()
		close(firstDone)
	}()
	require.Eventually(t, func() bool { return logger.GetLogLevelPattern() == "*:DEBUG" },
		time.Second, time.Millisecond, "the first session should apply its profile")

	secondDone := make(chan struct{})
	go func() {
		second.StartSendingBlocking()
		close(secondDone)
	}()
	require.Eventually(t, func() bool { return logger.GetLogLevelPattern() == "*:TRACE" },
		time.Second, time.Millisecond, "the second session should apply its profile")

	// The first session leaves while the second is still live. Nothing may be reverted yet:
	// the pre-refcount code restored *:DEBUG here, clobbering the live session's setting.
	close(releaseFirst)
	<-firstDone
	assert.Equal(t, "*:TRACE", logger.GetLogLevelPattern(),
		"a departing session must not revert the profile while another is still connected")

	close(releaseSecond)
	<-secondDone
	assert.Equal(t, baseline, logger.GetLogLevelPattern(),
		"the last session out must restore the profile the operator started with")
	assert.Equal(t, 0, logs.ProfileRefs())
}
