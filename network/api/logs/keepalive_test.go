package logs_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/api/logs"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// startSender stands up a real /log connection over an httptest server: the sender runs on the
// server-side gorilla conn with millisecond keepalive timings, and the returned channel closes
// when StartSendingBlocking returns — the moment the caller's release() hands the connection
// slot back. A real socket enforces read deadlines for free, which is the whole mechanism under
// test.
//
// The client always reads (gorilla only runs control-frame handlers while the app is reading),
// but answerPings decides whether it pongs. Not ponging is what a wedged peer looks like from
// the server: the socket is open, pings go out, nothing comes back.
func startSender(t *testing.T, answerPings bool) (<-chan struct{}, *websocket.Conn, func() int) {
	t.Helper()

	upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	marshalizer := &mock.MarshalizerStub{
		MarshalCalled: func(interface{}) ([]byte, error) { return []byte("log line"), nil },
	}

	returned := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			// t.Errorf (unlike Fatal/FailNow) is safe to call from any goroutine.
			t.Errorf("server failed to upgrade the websocket connection: %v", err)
			return
		}

		ls, err := logs.NewLogSender(marshalizer, conn, &mock.LoggerStub{}, false)
		if err != nil {
			t.Errorf("NewLogSender: %v", err)
			return
		}
		ls.SetKeepalive(20*time.Millisecond, 100*time.Millisecond, 100*time.Millisecond)

		go func() {
			ls.StartSendingBlocking()
			close(returned)
		}()
	}))
	t.Cleanup(srv.Close)

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(srv.URL, "http"), nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	var mut sync.Mutex
	pings := 0
	pong := conn.PingHandler()
	conn.SetPingHandler(func(appData string) error {
		mut.Lock()
		pings++
		mut.Unlock()

		if !answerPings {
			return nil
		}
		return pong(appData)
	})

	require.NoError(t, conn.WriteMessage(websocket.TextMessage, []byte(core.DefaultLogProfileIdentifier)))

	go func() {
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()

	return returned, conn, func() int {
		mut.Lock()
		defer mut.Unlock()

		return pings
	}
}

// TestLogSender_IdleConnectionIsReclaimedAtPongWait is the /log counterpart of
// TestClient_IdleConnectionReclaimedAtPongWait in websocket/hardening_test.go. A connection cap
// only holds if a peer that stops answering gives its slot back: before the keepalive, a client
// that completed the handshake and then went silent pinned its slot for the life of the process
// and every later /log dial got a 503 forever.
func TestLogSender_IdleConnectionIsReclaimedAtPongWait(t *testing.T) {
	t.Parallel()

	// The dead-but-connected peer: the socket is open, but no pong ever comes back.
	returned, _, pings := startSender(t, false)

	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("idle client was not reclaimed at pongWait; connection slot leaked")
	}

	assert.Positive(t, pings(), "the server should have tried to ping before giving up")
}

// TestLogSender_AnsweringTailerIsNotReclaimed is the other half, and the reason the handshake
// deadline is replaced by a rolling one rather than simply left armed: /log clients send nothing
// after the handshake, so a client that answers pings must survive well past pongWait. Without
// the pong re-arming the deadline this would kill every honest log tailer.
func TestLogSender_AnsweringTailerIsNotReclaimed(t *testing.T) {
	t.Parallel()

	// The honest tailer: silent, but it answers every ping.
	returned, conn, pings := startSender(t, true)

	// Several pongWait periods worth of being idle-but-responsive.
	select {
	case <-returned:
		t.Fatal("a client answering pings was disconnected; the keepalive kills honest tailers")
	case <-time.After(500 * time.Millisecond):
	}

	assert.Greater(t, pings(), 1, "the server should be pinging a live connection")

	// And it still tears down promptly once the peer really goes away.
	require.NoError(t, conn.Close())
	select {
	case <-returned:
	case <-time.After(2 * time.Second):
		t.Fatal("sender did not return after the connection closed")
	}
}
