package mock

import (
	"errors"
	"sync"
	"time"
)

// ErrWsConnClosed is what the stub returns from reads and writes once it has been closed,
// standing in for the "use of closed network connection" a real conn gives back.
var ErrWsConnClosed = errors.New("websocket connection is closed")

// ErrNoPongHandler is returned by Pong when the connection owner never registered a pong
// handler, so there is nothing for a delivered pong frame to do.
var ErrNoPongHandler = errors.New("no pong handler registered")

// maxRecordedCalls bounds the deadline history the stub keeps. A read loop driven by a stub
// handler that never fails spins as fast as the CPU allows, and an unbounded history turns
// that into an out-of-memory kill rather than a test failure. Assertions only ever look at
// the first few entries and at the most recent one, both of which survive the cap.
const maxRecordedCalls = 64

// WsConnStub -
type WsConnStub struct {
	mutHandlers         sync.Mutex
	closed              bool
	closeCalled         func() error
	readMessageCalled   func() (messageType int, p []byte, err error)
	writeMessageCalled  func(messageType int, data []byte) error
	readDeadlineCalled  func(t time.Time) error
	writeDeadlineCalled func(t time.Time) error
	pongHandler         func(appData string) error
	readDeadlines       []time.Time
	lastReadDeadline    time.Time
	writeDeadlines      []time.Time
	readLimit           int64
}

// record appends to a bounded history, keeping the earliest entries.
func record(history []time.Time, t time.Time) []time.Time {
	if len(history) >= maxRecordedCalls {
		return history
	}
	return append(history, t)
}

// Close -
func (wcs *WsConnStub) Close() error {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.closed = true
	if wcs.closeCalled == nil {
		return nil
	}

	return wcs.closeCalled()
}

// ReadMessage returns ErrWsConnClosed once the connection has been closed, the way a real
// conn does. Without that, a monitorConnection-style read loop never sees its connection go
// away and spins for the lifetime of the test binary.
// Handlers are invoked under mutHandlers, which serializes the state a test's handler closures
// capture across the read and write goroutines. A handler that blocks therefore blocks every
// other method on this connection — acceptable, but do not park in one indefinitely.
func (wcs *WsConnStub) ReadMessage() (messageType int, p []byte, err error) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	if wcs.closed {
		return 0, nil, ErrWsConnClosed
	}

	return wcs.readMessageCalled()
}

// WriteMessage -
func (wcs *WsConnStub) WriteMessage(messageType int, data []byte) error {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	if wcs.closed {
		return ErrWsConnClosed
	}

	return wcs.writeMessageCalled(messageType, data)
}

// SetReadMessageHandler -
func (wcs *WsConnStub) SetReadMessageHandler(f func() (messageType int, p []byte, err error)) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.readMessageCalled = f
}

// SetWriteMessageHandler -
func (wcs *WsConnStub) SetWriteMessageHandler(f func(messageType int, data []byte) error) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.writeMessageCalled = f
}

// SetCloseHandler -
func (wcs *WsConnStub) SetCloseHandler(f func() error) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.closeCalled = f
}

func (wcs *WsConnStub) SetReadDeadlineHandler(f func(t time.Time) error) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.readDeadlineCalled = f
}

// WriteControl -
func (wcs *WsConnStub) WriteControl(int, []byte, time.Time) error {
	return nil
}

// SetPongHandler -
func (wcs *WsConnStub) SetPongHandler(h func(appData string) error) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.pongHandler = h
}

// Pong invokes the handler the connection owner registered with SetPongHandler, standing in
// for gorilla delivering a pong control frame. With no handler registered it reports an error
// rather than succeeding silently: a caller that never called SetPongHandler has no keepalive,
// and a test asserting on the effect of a pong must fail rather than observe nothing happening.
func (wcs *WsConnStub) Pong() error {
	wcs.mutHandlers.Lock()
	handler := wcs.pongHandler
	wcs.mutHandlers.Unlock()

	if handler == nil {
		return ErrNoPongHandler
	}

	return handler("")
}

// SetReadLimit -
func (wcs *WsConnStub) SetReadLimit(limit int64) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.readLimit = limit
}

// SetReadDeadline -
func (wcs *WsConnStub) SetReadDeadline(t time.Time) error {
	wcs.mutHandlers.Lock()
	wcs.readDeadlines = record(wcs.readDeadlines, t)
	wcs.lastReadDeadline = t
	handler := wcs.readDeadlineCalled
	wcs.mutHandlers.Unlock()

	if handler == nil {
		return nil
	}

	return handler(t)
}

// ReadDeadlines returns the deadlines armed on this connection, capped at maxRecordedCalls.
func (wcs *WsConnStub) ReadDeadlines() []time.Time {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	return append([]time.Time{}, wcs.readDeadlines...)
}

// LastReadDeadline is the most recently armed read deadline, unaffected by the history cap.
func (wcs *WsConnStub) LastReadDeadline() time.Time {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	return wcs.lastReadDeadline
}

// ReadLimit -
func (wcs *WsConnStub) ReadLimit() int64 {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	return wcs.readLimit
}

// SetWriteDeadline -
func (wcs *WsConnStub) SetWriteDeadline(t time.Time) error {
	wcs.mutHandlers.Lock()
	wcs.writeDeadlines = record(wcs.writeDeadlines, t)
	handler := wcs.writeDeadlineCalled
	wcs.mutHandlers.Unlock()

	if handler == nil {
		return nil
	}

	return handler(t)
}

// SetWriteDeadlineHandler -
func (wcs *WsConnStub) SetWriteDeadlineHandler(f func(t time.Time) error) {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	wcs.writeDeadlineCalled = f
}

// WriteDeadlines -
func (wcs *WsConnStub) WriteDeadlines() []time.Time {
	wcs.mutHandlers.Lock()
	defer wcs.mutHandlers.Unlock()

	return append([]time.Time{}, wcs.writeDeadlines...)
}
