package libp2p_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/p2p"
	"github.com/klever-io/klever-go/network/p2p/data"
	"github.com/klever-io/klever-go/network/p2p/libp2p"
	"github.com/klever-io/klever-go/network/p2p/mock"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
)

var marshalizerOutput = []byte("marshalizer byte output")

// stubRemotePeerID is the peer on the other end of the stub stream's connection. handleStreams
// binds the declared auth pid to it, so a test that expects the association to be stored has to
// declare this pid.
const stubRemotePeerID = core.PeerID("remote ID")

func createStubHostForIdentityProvider() (*mock.ConnectableHostStub, network.Stream) {
	newStream := mock.NewStreamMock()
	newStream.SetConn(createStubConnForIdentityProvider())

	return &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
		NewStreamCalled: func(ctx context.Context, p peer.ID, pids ...protocol.ID) (stream network.Stream, e error) {
			return newStream, nil
		},
		IDCalled: func() peer.ID {
			return "stub ID"
		},
	}, newStream
}

func createStubConnForIdentityProvider() network.Conn {
	return &mock.ConnStub{
		RemotePeerCalled: func() peer.ID {
			return peer.ID(stubRemotePeerID)
		},
	}
}

func createStubMarshalizerForIdentityProvider() p2p.Marshalizer {
	return &mock.MarshalizerStub{
		MarshalCalled: func(obj interface{}) (bytes []byte, e error) {
			return marshalizerOutput, nil
		},
		UnmarshalCalled: func(obj interface{}, buff []byte) error {
			return nil
		},
	}
}

//------- NewIdentityProvider

func TestNewIdentityProvider_NilHostShouldErr(t *testing.T) {
	t.Parallel()

	ip, err := libp2p.NewIdentityProvider(
		nil,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{},
		&mock.MarshalizerStub{},
		time.Second,
	)

	assert.Nil(t, ip)
	assert.Equal(t, p2p.ErrNilHost, err)
}

func TestNewIdentityProvider_NilShardingCollectorStubShouldErr(t *testing.T) {
	t.Parallel()

	ip, err := libp2p.NewIdentityProvider(
		&mock.ConnectableHostStub{},
		nil,
		&mock.SignerVerifierStub{},
		&mock.MarshalizerStub{},
		time.Second,
	)

	assert.Nil(t, ip)
	assert.Equal(t, p2p.ErrNilNetworkShardingCollector, err)
}

func TestNewIdentityProvider_NilSignerVerifierShouldErr(t *testing.T) {
	t.Parallel()

	ip, err := libp2p.NewIdentityProvider(
		&mock.ConnectableHostStub{},
		&mock.NetworkShardingCollectorStub{},
		nil,
		&mock.MarshalizerStub{},
		time.Second,
	)

	assert.Nil(t, ip)
	assert.Equal(t, p2p.ErrNilSignerVerifier, err)
}

func TestNewIdentityProvider_NilMarshalizerErr(t *testing.T) {
	t.Parallel()

	ip, err := libp2p.NewIdentityProvider(
		&mock.ConnectableHostStub{},
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{},
		nil,
		time.Second,
	)

	assert.Nil(t, ip)
	assert.Equal(t, p2p.ErrNilMarshalizer, err)
}

func TestNewIdentityProvider_ShouldWorkAndSetStreamHandler(t *testing.T) {
	t.Parallel()

	setStreamHandlerCalled := false
	ip, err := libp2p.NewIdentityProvider(
		&mock.ConnectableHostStub{
			SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {
				setStreamHandlerCalled = true
			},
		},
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{},
		&mock.MarshalizerStub{},
		time.Second,
	)

	assert.NotNil(t, ip)
	assert.Nil(t, err)
	assert.True(t, setStreamHandlerCalled)
}

//------- Connected

func TestIdentityProvider_ConnectedMarshalizerFailShouldNotPanic(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r != nil {
			assert.Fail(t, fmt.Sprintf("should have not fail: %v", r))
		}
	}()

	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{
			PublicKeyCalled: func() []byte {
				return []byte("pub key")
			},
		},
		&mock.MarshalizerStub{
			MarshalCalled: func(obj interface{}) (bytes []byte, e error) {
				return nil, errors.New("marshalizer error")
			},
		},
		time.Second,
	)

	ip.Connected(nil, createStubConnForIdentityProvider())

	time.Sleep(time.Millisecond * 100)
}

func TestIdentityProvider_ConnectedSignFailShouldNotPanic(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r != nil {
			assert.Fail(t, fmt.Sprintf("should have not fail: %v", r))
		}
	}()

	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{
			PublicKeyCalled: func() []byte {
				return []byte("pub key")
			},
			SignCalled: func(message []byte) (bytes []byte, e error) {
				return nil, errors.New("signing failed")
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)

	ip.Connected(nil, createStubConnForIdentityProvider())

	time.Sleep(time.Millisecond * 100)
}

func TestIdentityProvider_ConnectedShouldWrite(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r != nil {
			assert.Fail(t, fmt.Sprintf("should have not fail: %v", r))
		}
	}()

	host, stream := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{
			PublicKeyCalled: func() []byte {
				return []byte("pub key")
			},
			SignCalled: func(message []byte) (bytes []byte, e error) {
				return []byte("signature"), nil
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)

	ip.Connected(nil, createStubConnForIdentityProvider())

	time.Sleep(time.Millisecond * 100)

	recovered := make([]byte, len(marshalizerOutput))
	_, _ = stream.Read(recovered)
	assert.Equal(t, marshalizerOutput, recovered)
}

//------- processReceivedData

func TestIdentityProvider_ProcessReceivedDataUnmarshalFailsShouldError(t *testing.T) {
	t.Parallel()

	errExpected := errors.New("expected error")
	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{},
		&mock.MarshalizerStub{
			UnmarshalCalled: func(obj interface{}, buff []byte) error {
				return errExpected
			},
		},
		time.Second,
	)

	err := ip.ProcessReceivedData(make([]byte, 0), core.PeerID(""))

	assert.Equal(t, errExpected, err)
}

func TestIdentityProvider_ProcessReceivedDataMarshalFailsShouldError(t *testing.T) {
	t.Parallel()

	errExpected := errors.New("expected error")
	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{},
		&mock.MarshalizerStub{
			MarshalCalled: func(obj interface{}) (bytes []byte, e error) {
				return make([]byte, 0), errExpected
			},
			UnmarshalCalled: func(obj interface{}, buff []byte) error {
				return nil
			},
		},
		time.Second,
	)

	err := ip.ProcessReceivedData(make([]byte, 0), core.PeerID(""))

	assert.Equal(t, errExpected, err)
}

func TestIdentityProvider_ProcessReceivedDataSignerErrorsShouldError(t *testing.T) {
	t.Parallel()

	errExpected := errors.New("expected error")
	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return errExpected
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)

	err := ip.ProcessReceivedData(make([]byte, 0), core.PeerID(""))

	assert.Equal(t, errExpected, err)
}

func TestIdentityProvider_ProcessReceivedDataShouldUpdateCollector(t *testing.T) {
	t.Parallel()

	updateWasCalled := false
	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updateWasCalled = true
			},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return nil
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)

	err := ip.ProcessReceivedData(make([]byte, 0), core.PeerID(""))

	assert.Nil(t, err)
	assert.True(t, updateWasCalled)
}

// createAuthMarshalizerForIdentityProvider returns a marshalizer whose Unmarshal populates the
// AuthMessage with a caller-chosen declared pid, so the pid binding can actually be exercised.
func createAuthMarshalizerForIdentityProvider(declaredPid core.PeerID) p2p.Marshalizer {
	return &mock.MarshalizerStub{
		MarshalCalled: func(obj interface{}) (bytes []byte, e error) {
			return marshalizerOutput, nil
		},
		UnmarshalCalled: func(obj interface{}, buff []byte) error {
			am, ok := obj.(*data.AuthMessage)
			if !ok {
				return errors.New("unexpected type passed to Unmarshal")
			}

			am.Message = declaredPid.Bytes()
			am.Pubkey = []byte("sender pk")
			am.Sig = []byte("sig")

			return nil
		},
	}
}

// An auth message is signed by whoever sends it, and the signature covers the sender's own choice
// of AuthMessage.Message. Without binding that field to the stream's remote peer, anyone can open a
// klever-node-auth stream, declare a validator's pid and sign with a throwaway key: the association
// would be re-pointed at the throwaway pk and the real validator would stop resolving as
// ValidatorPeer, so IsOriginatorElectedForTopic would then reject it from validator-only topics.
// Same invariant as the heartbeat check in KLR-48, in the demote direction.
func TestIdentityProvider_ProcessReceivedDataForeignPidShouldErrorAndNotUpdateCollector(t *testing.T) {
	t.Parallel()

	victimPid := core.PeerID("a validator pid")
	attackerPid := core.PeerID("an attacker pid")

	updateWasCalled := false
	verifyWasCalled := false
	removedPid := core.PeerID("")

	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updateWasCalled = true
			},
			RemovePeerIDAssociationCalled: func(pid core.PeerID) {
				removedPid = pid
			},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				verifyWasCalled = true
				// a real attacker signs with their own key, so the signature is valid
				return nil
			},
		},
		createAuthMarshalizerForIdentityProvider(victimPid),
		time.Second,
	)

	err := ip.ProcessReceivedData(make([]byte, 0), attackerPid)

	assert.True(t, errors.Is(err, p2p.ErrAuthPidMismatch))
	assert.False(t, updateWasCalled, "the forged association must never be stored")
	assert.False(t, verifyWasCalled, "a foreign pid is rejected before the signature check")
	assert.Equal(t, attackerPid, removedPid,
		"the sender's own association is dropped, never the declared victim's")
}

func TestIdentityProvider_ProcessReceivedDataOwnPidShouldUpdateCollector(t *testing.T) {
	t.Parallel()

	senderPid := core.PeerID("the sender pid")

	updatedPid := core.PeerID("")
	removeWasCalled := false

	host, _ := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updatedPid = pid
			},
			RemovePeerIDAssociationCalled: func(pid core.PeerID) {
				removeWasCalled = true
			},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return nil
			},
		},
		createAuthMarshalizerForIdentityProvider(senderPid),
		time.Second,
	)

	err := ip.ProcessReceivedData(make([]byte, 0), senderPid)

	assert.Nil(t, err)
	assert.Equal(t, senderPid, updatedPid)
	assert.False(t, removeWasCalled)
}

//------- handleStreams

func TestIdentityProvider_HandleStreamsReceivedMessageShouldUpdateCollector(t *testing.T) {
	t.Parallel()

	updateWasCalled := false
	host, stream := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updateWasCalled = true
			},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return nil
			},
		},
		createAuthMarshalizerForIdentityProvider(stubRemotePeerID),
		time.Second,
	)
	_, _ = stream.Write([]byte("mock data"))

	ip.HandleStreams(stream)

	assert.True(t, updateWasCalled)
}

func TestIdentityProvider_HandleStreamsTimeoutShouldNotFailOrWrite(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r != nil {
			assert.Fail(t, fmt.Sprintf("should have not fail: %v", r))
		}
	}()

	updateWasCalled := false
	host, stream := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updateWasCalled = true
			},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return nil
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)

	ip.HandleStreams(stream)

	assert.False(t, updateWasCalled)
}

func TestIdentityProvider_HandleStreamsClosedStreamShouldNotFailOrWrite(t *testing.T) {
	t.Parallel()

	defer func() {
		r := recover()
		if r != nil {
			assert.Fail(t, fmt.Sprintf("should have not fail: %v", r))
		}
	}()

	updateWasCalled := false
	host, stream := createStubHostForIdentityProvider()
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updateWasCalled = true
			},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return nil
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)
	_ = stream.Close()

	ip.HandleStreams(stream)

	assert.False(t, updateWasCalled)
}

func TestIdentityProvider_HandleStreamsTimeoutShouldResetStream(t *testing.T) {
	t.Parallel()

	newStream := mock.NewStreamMock()
	host := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
		IDCalled: func() peer.ID {
			return "stub ID"
		},
	}
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {},
		},
		&mock.SignerVerifierStub{},
		createStubMarshalizerForIdentityProvider(),
		time.Millisecond*100,
	)

	ip.HandleStreams(newStream)

	// On timeout the stalled stream must be reset — otherwise the peer holds it open forever and
	// the abandoned reader goroutine leaks blocked on it (KLC-2433 residual hardening).
	assert.True(t, newStream.IsReset(),
		"a stream that timed out without delivering data must be reset (RST), not merely closed")
}

func TestIdentityProvider_HandleStreamsSuccessShouldCloseStream(t *testing.T) {
	t.Parallel()

	newStream := mock.NewStreamMock()
	newStream.SetConn(createStubConnForIdentityProvider())
	host := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
		IDCalled: func() peer.ID {
			return "stub ID"
		},
	}
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {},
		},
		&mock.SignerVerifierStub{
			VerifyCalled: func(message []byte, sig []byte, pk []byte) error {
				return nil
			},
		},
		createAuthMarshalizerForIdentityProvider(stubRemotePeerID),
		time.Second,
	)
	_, _ = newStream.Write([]byte("mock data"))

	ip.HandleStreams(newStream)

	// The auth exchange is one-shot: after the payload is processed the stream must be closed so
	// a peer cannot park it open for the connection's lifetime (KLC-2433 residual hardening).
	assert.True(t, newStream.IsClosed(),
		"a stream whose payload was processed must be closed")
	assert.False(t, newStream.IsReset(),
		"a successful exchange must close (FIN) so the peer keeps its half, not reset (RST)")
}

func TestIdentityProvider_ConnectedShouldCloseStreamAfterWrite(t *testing.T) {
	t.Parallel()

	newStream := mock.NewStreamMock()
	host := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
		NewStreamCalled: func(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error) {
			return newStream, nil
		},
		IDCalled: func() peer.ID {
			return "stub ID"
		},
	}
	ip, _ := libp2p.NewIdentityProvider(
		host,
		&mock.NetworkShardingCollectorStub{},
		&mock.SignerVerifierStub{
			PublicKeyCalled: func() []byte {
				return []byte("pub key")
			},
			SignCalled: func(message []byte) ([]byte, error) {
				return []byte("signature"), nil
			},
		},
		createStubMarshalizerForIdentityProvider(),
		time.Second,
	)

	ip.Connected(nil, createStubConnForIdentityProvider())

	// The sender wrote its one-shot payload; the stream must then be closed instead of being
	// held open for the connection's lifetime (KLC-2433 residual hardening).
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && !newStream.IsClosed() {
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, newStream.IsClosed(),
		"the outbound auth stream must be closed after the payload is written")
}
