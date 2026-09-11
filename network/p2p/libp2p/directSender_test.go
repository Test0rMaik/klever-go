package libp2p_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"testing"
	"time"

	ggio "github.com/gogo/protobuf/io"
	"github.com/klever-io/klever-go/config"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/network/p2p"
	"github.com/klever-io/klever-go/network/p2p/libp2p"
	"github.com/klever-io/klever-go/network/p2p/mock"
	"github.com/klever-io/klever-go/tools/check"
	pubsub "github.com/libp2p/go-libp2p-pubsub"
	pubsub_pb "github.com/libp2p/go-libp2p-pubsub/pb"
	libp2pCrypto "github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/network"
	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/libp2p/go-libp2p/core/protocol"
	"github.com/stretchr/testify/assert"
)

const timeout = time.Second * 5

var blankMessageHandler = func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
	return nil
}

// The topic-processor checker is a required constructor argument, so every directSender under test
// needs one. Permissive by default; tests that care about the gate pass their own.
var allTopicsRegistered = func(topic string) bool {
	return true
}

func generateHostStub() *mock.ConnectableHostStub {
	return &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
	}
}

func createConnStub(stream network.Stream, id peer.ID, sk libp2pCrypto.PrivKey, remotePeer peer.ID) *mock.ConnStub {
	return &mock.ConnStub{
		GetStreamsCalled: func() []network.Stream {
			if stream == nil {
				return make([]network.Stream, 0)
			}

			return []network.Stream{stream}
		},
		LocalPeerCalled: func() peer.ID {
			return id
		},
		LocalPrivateKeyCalled: func() libp2pCrypto.PrivKey {
			return sk
		},
		RemotePeerCalled: func() peer.ID {
			return remotePeer
		},
	}
}

func createLibP2PCredentialsDirectSender() (peer.ID, libp2pCrypto.PrivKey) {
	sk, _, _ := libp2pCrypto.GenerateSecp256k1Key(rand.Reader)

	id, _ := peer.IDFromPublicKey(sk.GetPublic())

	return id, sk
}

//------- NewDirectSender

func TestNewDirectSender_NilContextShouldErr(t *testing.T) {
	hs := &mock.ConnectableHostStub{}

	var ctx context.Context = nil
	ds, err := libp2p.NewDirectSender(ctx, hs, func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
		return nil
	})

	assert.True(t, check.IfNil(ds))
	assert.Equal(t, p2p.ErrNilContext, err)
}

func TestNewDirectSender_NilHostShouldErr(t *testing.T) {
	ds, err := libp2p.NewDirectSender(context.Background(), nil, func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
		return nil
	})

	assert.True(t, check.IfNil(ds))
	assert.Equal(t, p2p.ErrNilHost, err)
}

func TestNewDirectSender_NilMessageHandlerShouldErr(t *testing.T) {
	ds, err := libp2p.NewDirectSender(context.Background(), generateHostStub(), nil)

	assert.True(t, check.IfNil(ds))
	assert.Equal(t, p2p.ErrNilDirectSendMessageHandler, err)
}

func TestNewDirectSender_NilTopicProcessorCheckerShouldErr(t *testing.T) {
	ds, err := libp2p.NewDirectSender(context.Background(), generateHostStub(), blankMessageHandler)

	assert.True(t, check.IfNil(ds))
	assert.True(t, errors.Is(err, p2p.ErrNilValidator))
}

func TestNewDirectSender_OkValsShouldWork(t *testing.T) {
	ds, err := libp2p.NewDirectSender(context.Background(), generateHostStub(), func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
		return nil
	}, libp2p.WithTopicProcessorChecker(allTopicsRegistered))

	assert.False(t, check.IfNil(ds))
	assert.Nil(t, err)
}

func TestNewDirectSender_OkValsShouldCallSetStreamHandlerWithCorrectValues(t *testing.T) {
	var pidCalled protocol.ID
	var handlerCalled network.StreamHandler

	hs := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {
			pidCalled = pid
			handlerCalled = handler
		},
	}

	_, _ = libp2p.NewDirectSender(context.Background(), hs, func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
		return nil
	}, libp2p.WithTopicProcessorChecker(allTopicsRegistered))

	assert.NotNil(t, handlerCalled)
	assert.Equal(t, libp2p.DirectSendID, pidCalled)
}

func TestNewDirectSender_UnsetInboundStreamCapsKeepBuiltInDefaults(t *testing.T) {
	ds, err := libp2p.NewDirectSenderWithConfig(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		config.DirectSendConfig{},
	)
	assert.Nil(t, err)

	perPeer, total := ds.InboundStreamCaps()
	assert.Equal(t, 4, perPeer, "an unset per-peer cap must keep the built-in default")
	assert.Equal(t, 512, total, "an unset node-wide cap must keep the built-in default")

	ds, err = libp2p.NewDirectSenderWithConfig(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		config.DirectSendConfig{MaxInboundStreamsPerPeer: 9},
	)
	assert.Nil(t, err)

	perPeer, total = ds.InboundStreamCaps()
	assert.Equal(t, 9, perPeer)
	assert.Equal(t, 512, total, "a knob left at 0 must keep its built-in default")
}

func TestNewDirectSender_ConfiguredInboundStreamCapsAreApplied(t *testing.T) {
	ds, err := libp2p.NewDirectSenderWithConfig(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		config.DirectSendConfig{
			MaxInboundStreamsPerPeer: 9,
			MaxInboundStreamsTotal:   64,
		},
	)
	assert.Nil(t, err)

	perPeer, total := ds.InboundStreamCaps()
	assert.Equal(t, 9, perPeer)
	assert.Equal(t, 64, total)
}

//------- ValidateDirectMessage

func TestDirectSender_ValidateDirectMessageNilMessageShouldErr(t *testing.T) {
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	err := ds.ValidateDirectMessage(nil, "peer id")

	assert.Equal(t, p2p.ErrNilMessage, err)
}

func TestDirectSender_ValidateDirectMessageNilTopicIdsShouldErr(t *testing.T) {
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = []byte("111")
	msg.From = []byte(id)
	msg.Topic = nil

	err := ds.ValidateDirectMessage(msg, id)

	assert.Equal(t, p2p.ErrNilTopic, err)
}

func TestDirectSender_ValidateDirectMessageAlreadySeenMsgShouldErr(t *testing.T) {
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = []byte("11111111")
	msg.From = []byte(id)
	topic := "topic"
	msg.Topic = &topic

	ds.MarkSeen(msg.GetFrom(), msg.GetSeqno())

	err := ds.ValidateDirectMessage(msg, id)

	assert.Equal(t, p2p.ErrAlreadySeenMessage, err)
}

func TestDirectSender_ValidateDirectMessageUnexpectedSeqnoLengthShouldErr(t *testing.T) {
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = bytes.Repeat([]byte{1}, 1024)
	msg.From = []byte(id)
	topic := "topic"
	msg.Topic = &topic

	err := ds.ValidateDirectMessage(msg, id)

	assert.True(t, errors.Is(err, p2p.ErrInvalidValue))
	assert.Equal(t, 0, ds.SeenMessagesLen(), "a rejected seqno must not be recorded")

	msg.Seqno = []byte("111")

	err = ds.ValidateDirectMessage(msg, id)

	assert.True(t, errors.Is(err, p2p.ErrInvalidValue))
	assert.Equal(t, 0, ds.SeenMessagesLen(), "a rejected seqno must not be recorded")
}

func TestDirectSender_ValidateDirectMessageShouldWork(t *testing.T) {
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = []byte("11111111")
	msg.From = []byte(id)
	topic := "topic"
	msg.Topic = &topic

	err := ds.ValidateDirectMessage(msg, id)

	assert.Nil(t, err)
}

//------- SendDirectToConnectedPeer

func TestDirectSender_SendDirectToConnectedPeerBufferToLargeShouldErr(t *testing.T) {
	netw := &mock.NetworkStub{}

	id, sk := createLibP2PCredentialsDirectSender()
	remotePeer := peer.ID("remote peer")

	stream := mock.NewStreamMock()
	err := stream.SetProtocol(libp2p.DirectSendID)
	assert.Nil(t, err)

	cs := createConnStub(stream, id, sk, remotePeer)

	netw.ConnsToPeerCalled = func(p peer.ID) []network.Conn {
		return []network.Conn{cs}
	}

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		&mock.ConnectableHostStub{
			SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
			NetworkCalled: func() network.Network {
				return netw
			},
		},
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	messageTooLarge := bytes.Repeat([]byte{65}, libp2p.MaxSendBuffSize)

	err = ds.Send("topic", messageTooLarge, core.PeerID(cs.RemotePeer()))

	assert.True(t, errors.Is(err, p2p.ErrMessageTooLarge))
}

func TestDirectSender_SendDirectToConnectedPeerNotConnectedPeerShouldErr(t *testing.T) {
	netw := &mock.NetworkStub{
		ConnsToPeerCalled: func(p peer.ID) []network.Conn {
			return make([]network.Conn, 0)
		},
	}

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		&mock.ConnectableHostStub{
			SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
			NetworkCalled: func() network.Network {
				return netw
			},
		},
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	err := ds.Send("topic", []byte("data"), "not connected peer")

	assert.Equal(t, p2p.ErrPeerNotDirectlyConnected, err)
}

func TestDirectSender_SendDirectToConnectedPeerNewStreamErrorsShouldErr(t *testing.T) {
	t.Parallel()

	netw := &mock.NetworkStub{}

	hs := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
		NetworkCalled: func() network.Network {
			return netw
		},
	}

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		hs,
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, sk := createLibP2PCredentialsDirectSender()
	remotePeer := peer.ID("remote peer")
	errNewStream := errors.New("new stream error")

	cs := createConnStub(nil, id, sk, remotePeer)

	netw.ConnsToPeerCalled = func(p peer.ID) []network.Conn {
		return []network.Conn{cs}
	}

	hs.NewStreamCalled = func(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error) {
		return nil, errNewStream
	}

	data := []byte("data")
	topic := "topic"
	err := ds.Send(topic, data, core.PeerID(cs.RemotePeer()))

	assert.Equal(t, errNewStream, err)
}

func TestDirectSender_SendDirectToConnectedPeerExistingStreamShouldSendToStream(t *testing.T) {
	netw := &mock.NetworkStub{}

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		&mock.ConnectableHostStub{
			SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
			NetworkCalled: func() network.Network {
				return netw
			},
		},
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, sk := createLibP2PCredentialsDirectSender()
	remotePeer := peer.ID("remote peer")

	stream := mock.NewStreamMock()
	err := stream.SetProtocol(libp2p.DirectSendID)
	assert.Nil(t, err)

	cs := createConnStub(stream, id, sk, remotePeer)

	netw.ConnsToPeerCalled = func(p peer.ID) []network.Conn {
		return []network.Conn{cs}
	}

	receivedMsg := &pubsub_pb.Message{}
	chanDone := make(chan bool)

	go func(s network.Stream) {
		reader := ggio.NewDelimitedReader(s, 1<<20)
		for {
			err := reader.ReadMsg(receivedMsg)
			if err != nil {
				fmt.Println(err.Error())
				return
			}

			chanDone <- true
		}
	}(stream)

	data := []byte("data")
	topic := "topic"
	err = ds.Send(topic, data, core.PeerID(cs.RemotePeer()))
	assert.Nil(t, err)

	select {
	case <-chanDone:
	case <-time.After(timeout):
		assert.Fail(t, "timeout getting data from stream")
		return
	}

	assert.Equal(t, data, receivedMsg.Data)
	assert.Equal(t, topic, *receivedMsg.Topic)
}

func TestDirectSender_SendDirectToConnectedPeerNewStreamShouldSendToStream(t *testing.T) {
	netw := &mock.NetworkStub{}

	hs := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {},
		NetworkCalled: func() network.Network {
			return netw
		},
	}

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		hs,
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, sk := createLibP2PCredentialsDirectSender()
	remotePeer := peer.ID("remote peer")

	stream := mock.NewStreamMock()
	err := stream.SetProtocol(libp2p.DirectSendID)
	assert.Nil(t, err)

	cs := createConnStub(stream, id, sk, remotePeer)

	netw.ConnsToPeerCalled = func(p peer.ID) []network.Conn {
		return []network.Conn{cs}
	}

	hs.NewStreamCalled = func(ctx context.Context, p peer.ID, pids ...protocol.ID) (network.Stream, error) {
		if p == remotePeer && pids[0] == libp2p.DirectSendID {
			return stream, nil
		}
		return nil, errors.New("wrong parameters")
	}

	receivedMsg := &pubsub_pb.Message{}
	chanDone := make(chan bool)

	go func(s network.Stream) {
		reader := ggio.NewDelimitedReader(s, 1<<20)
		for {
			err := reader.ReadMsg(receivedMsg)
			if err != nil {
				fmt.Println(err.Error())
				return
			}

			chanDone <- true
		}
	}(stream)

	data := []byte("data")
	topic := "topic"
	err = ds.Send(topic, data, core.PeerID(cs.RemotePeer()))

	select {
	case <-chanDone:
	case <-time.After(timeout):
		assert.Fail(t, "timeout getting data from stream")
		return
	}

	assert.Nil(t, err)
	assert.Equal(t, data, receivedMsg.Data)
	assert.Equal(t, topic, *receivedMsg.Topic)
}

//------- received messages tests

func TestDirectSender_ReceivedSentMessageShouldCallMessageHandlerTestFullCycle(t *testing.T) {
	var streamHandler network.StreamHandler
	netw := &mock.NetworkStub{}

	hs := &mock.ConnectableHostStub{
		SetStreamHandlerCalled: func(pid protocol.ID, handler network.StreamHandler) {
			streamHandler = handler
		},
		NetworkCalled: func() network.Network {
			return netw
		},
	}

	var receivedMsg *pubsub.Message
	chanDone := make(chan bool)

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		hs,
		func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
			receivedMsg = msg
			chanDone <- true
			return nil
		},
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, sk := createLibP2PCredentialsDirectSender()
	remotePeer := peer.ID("remote peer")

	stream := mock.NewStreamMock()
	stream.SetConn(
		&mock.ConnStub{
			RemotePeerCalled: func() peer.ID {
				return remotePeer
			},
		})
	err := stream.SetProtocol(libp2p.DirectSendID)
	assert.Nil(t, err)

	streamHandler(stream)

	cs := createConnStub(stream, id, sk, remotePeer)

	netw.ConnsToPeerCalled = func(p peer.ID) []network.Conn {
		return []network.Conn{cs}
	}
	cs.LocalPeerCalled = func() peer.ID {
		return cs.RemotePeer()
	}

	data := []byte("data")
	topic := "topic"
	_ = ds.Send(topic, data, core.PeerID(cs.RemotePeer()))

	select {
	case <-chanDone:
	case <-time.After(timeout):
		assert.Fail(t, "timeout")
		return
	}

	assert.NotNil(t, receivedMsg)
	assert.Equal(t, data, receivedMsg.Data)
	assert.Equal(t, topic, *receivedMsg.Topic)
}

func TestDirectSender_ValidateDirectMessageFromMismatchesFromConnectedPeerShouldErr(t *testing.T) {
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(allTopicsRegistered),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = []byte("111")
	msg.From = []byte(id)
	topic := "topic"
	msg.Topic = &topic

	err := ds.ValidateDirectMessage(msg, "not the same peer id")

	assert.True(t, errors.Is(err, p2p.ErrInvalidValue))
}

// ------- directStreamHandler

func TestDirectSender_DirectStreamHandlerFailedReadDeadlineShouldFailClosed(t *testing.T) {
	ds, err := libp2p.NewDirectSender(context.Background(), generateHostStub(), blankMessageHandler, libp2p.WithTopicProcessorChecker(allTopicsRegistered))
	assert.Nil(t, err)

	stream := mock.NewStreamMock()
	stream.SetConn(&mock.ConnStub{RemotePeerCalled: func() peer.ID {
		return "attacker"
	}})
	stream.SetReadDeadlineError(errors.New("deadline unsupported"))

	ds.DirectStreamHandler(stream)

	// Without an enforceable read deadline the reader cannot be bounded (KLC-2433 F3), so the
	// stream must be reset instead of silently running unbounded.
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) && !stream.IsClosed() {
		time.Sleep(10 * time.Millisecond)
	}
	assert.True(t, stream.IsReset(),
		"stream with an unenforceable read deadline must be reset (fail closed)")
}

// An unknown topic must not consume a replay-cache entry, but validation must still pass so the
// frame reaches directMessageHandler: that is where the payload is unmarshalled, and a malformed
// payload has to keep earning its blacklist even on a topic nobody serves.
func TestDirectSender_ValidateDirectMessageUnknownTopicDoesNotConsumeReplayCacheEntry(t *testing.T) {
	t.Parallel()

	handlerCalled := false
	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		func(msg *pubsub.Message, fromConnectedPeer core.PeerID) error {
			handlerCalled = true
			return nil
		},
		libp2p.WithTopicProcessorChecker(func(topic string) bool { return topic == "known" }),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = []byte("11111111")
	msg.From = []byte(id)
	topic := "unknown"
	msg.Topic = &topic

	err := ds.ValidateDirectMessage(msg, id)

	assert.Nil(t, err)
	assert.False(t, ds.HasSeen(msg.GetFrom(), msg.GetSeqno()))
	assert.Equal(t, 0, ds.SeenMessagesLen())

	stream := mock.NewStreamMock()
	stream.SetConn(&mock.ConnStub{RemotePeerCalled: func() peer.ID { return id }})
	_ = ds.ProcessInboundFrame(msg, stream)

	assert.True(t, handlerCalled, "an unknown-topic frame must still reach the handler")
}

func TestDirectSender_ValidateDirectMessageKnownTopicMarksReplayCacheEntry(t *testing.T) {
	t.Parallel()

	ds, _ := libp2p.NewDirectSender(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		libp2p.WithTopicProcessorChecker(func(topic string) bool { return topic == "known" }),
	)

	id, _ := createLibP2PCredentialsDirectSender()

	msg := &pubsub_pb.Message{}
	msg.Data = []byte("data")
	msg.Seqno = []byte("11111111")
	msg.From = []byte(id)
	topic := "known"
	msg.Topic = &topic

	err := ds.ValidateDirectMessage(msg, id)

	assert.Nil(t, err)
	assert.True(t, ds.HasSeen(msg.GetFrom(), msg.GetSeqno()))
	assert.Equal(t, 1, ds.SeenMessagesLen())
}

func TestDirectSender_FloodingPeerDoesNotEvictAnotherPeersReplayEntry(t *testing.T) {
	t.Parallel()

	// room for exactly the two peers, so the flooder gets a bucket and has to overflow its own;
	// sized through the config path, the same way production sizes it
	ds, err := libp2p.NewDirectSenderWithConfig(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		config.DirectSendConfig{MaxSeenMessages: 2 * libp2p.SeenMessagesPerPeer},
	)
	if err != nil {
		t.Fatal(err)
	}

	victim, _ := createLibP2PCredentialsDirectSender()
	flooder, _ := createLibP2PCredentialsDirectSender()

	seqno := []byte("11111111")
	ds.MarkSeen([]byte(victim), seqno)

	for i := 0; i < libp2p.SeenMessagesPerPeer*4; i++ {
		fresh := make([]byte, 8)
		binary.BigEndian.PutUint64(fresh, uint64(i))
		ds.MarkSeen([]byte(flooder), fresh)
	}

	assert.True(t, ds.HasSeen([]byte(victim), seqno))
}

// TestNewDirectSender_ConfiguredMaxSeenMessagesIsApplied pins that the configured value reaches
// the cache. Three peers against a cap sized for two: the third is refused a bucket, so its
// frames are not counted. Under the 131072 default all three would be tracked and the count
// would be 30 — which is what dropping the config wiring produced before this test existed.
func TestNewDirectSender_ConfiguredMaxSeenMessagesIsApplied(t *testing.T) {
	ds, err := libp2p.NewDirectSenderWithConfig(
		context.Background(),
		generateHostStub(),
		blankMessageHandler,
		config.DirectSendConfig{MaxSeenMessages: 2 * libp2p.SeenMessagesPerPeer},
	)
	if err != nil {
		t.Fatal(err)
	}

	for p := 0; p < 3; p++ {
		peerID, _ := createLibP2PCredentialsDirectSender()
		for i := 0; i < 10; i++ {
			seqno := make([]byte, 8)
			binary.BigEndian.PutUint64(seqno, uint64(p*100+i))
			ds.MarkSeen([]byte(peerID), seqno)
		}
	}

	assert.Equal(t, 20, ds.SeenMessagesLen(),
		"a cap sized for two peers must track exactly two; a third counted means the configured value never reached the cache")
}
