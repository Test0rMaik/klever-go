package process_test

import (
	"bytes"
	"errors"
	"testing"

	testscommon "github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/node/heartbeat"
	"github.com/klever-io/klever-go/node/heartbeat/data"
	"github.com/klever-io/klever-go/node/heartbeat/mock"
	"github.com/klever-io/klever-go/node/heartbeat/process"
	"github.com/klever-io/klever-go/sharding"
	"github.com/klever-io/klever-go/sharding/networksharding"
	"github.com/stretchr/testify/assert"
)

func CreateHeartbeat() *data.Heartbeat {
	hb := data.Heartbeat{
		Payload:         []byte("Payload"),
		Pubkey:          []byte("PubKey"),
		Signature:       []byte("Signature"),
		VersionNumber:   "VersionNumber",
		NodeDisplayName: "NodeDisplayName",
	}
	return &hb
}

func TestNewMessageProcessor_PeerSignatureHandlerNilShouldErr(t *testing.T) {
	t.Parallel()

	mon, err := process.NewMessageProcessor(
		nil,
		&mock.MarshalizerStub{},
		&mock.NetworkShardingCollectorStub{},
	)

	assert.Nil(t, mon)
	assert.Equal(t, heartbeat.ErrNilPeerSignatureHandler, err)
}

func TestNewMessageProcessor_MarshalizerNilShouldErr(t *testing.T) {
	t.Parallel()

	mon, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		nil,
		&mock.NetworkShardingCollectorStub{},
	)

	assert.Nil(t, mon)
	assert.Equal(t, heartbeat.ErrNilMarshalizer, err)
}

func TestNewMessageProcessor_NetworkShardingCollectorNilShouldErr(t *testing.T) {
	t.Parallel()

	mon, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		&mock.MarshalizerStub{},
		nil,
	)

	assert.Nil(t, mon)
	assert.Equal(t, heartbeat.ErrNilNetworkShardingCollector, err)
}

func TestNewMessageProcessor_ShouldWork(t *testing.T) {
	t.Parallel()

	mon, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		&mock.MarshalizerStub{},
		&mock.NetworkShardingCollectorStub{},
	)

	assert.Nil(t, err)
	assert.NotNil(t, mon)
	assert.False(t, mon.IsInterfaceNil())
}

func TestNewMessageProcessor_VerifyMessageAllSmallerShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_VerifyMessageAllNilShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Signature = nil
	hbmi.Payload = nil
	hbmi.Pubkey = nil
	hbmi.VersionNumber = ""
	hbmi.NodeDisplayName = ""

	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_VerifyMessageBiggerPublicKeyShouldErr(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Pubkey = make([]byte, process.GetMaxSizeInBytes()+1)
	err := process.VerifyLengths(hbmi)

	assert.NotNil(t, err)
}

func TestNewMessageProcessor_VerifyMessageAllSmallerPublicKeyShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Pubkey = make([]byte, process.GetMaxSizeInBytes())
	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_VerifyMessageBiggerPayloadShouldErr(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Payload = make([]byte, process.GetMaxSizeInBytes()+1)
	err := process.VerifyLengths(hbmi)

	assert.NotNil(t, err)
}

func TestNewMessageProcessor_VerifyMessageSmallerPayloadShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Payload = make([]byte, process.GetMaxSizeInBytes())
	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_VerifyMessageBiggerSignatureShouldErr(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Signature = make([]byte, process.GetMaxSizeInBytes()+1)
	err := process.VerifyLengths(hbmi)

	assert.NotNil(t, err)
}

func TestNewMessageProcessor_VerifyMessageSignatureShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.Signature = make([]byte, process.GetMaxSizeInBytes()-1)
	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_VerifyMessageBiggerNodeDisplayNameShouldErr(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.NodeDisplayName = string(make([]byte, process.GetMaxSizeInBytes()+1))
	err := process.VerifyLengths(hbmi)

	assert.NotNil(t, err)
}

func TestNewMessageProcessor_VerifyMessageNodeDisplayNameShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.NodeDisplayName = string(make([]byte, process.GetMaxSizeInBytes()))
	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_VerifyMessageBiggerVersionNumberShouldErr(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.VersionNumber = string(make([]byte, process.GetMaxSizeInBytes()+1))
	err := process.VerifyLengths(hbmi)

	assert.NotNil(t, err)
}

func TestNewMessageProcessor_VerifyMessageVersionNumberShouldWork(t *testing.T) {
	t.Parallel()

	hbmi := CreateHeartbeat()
	hbmi.VersionNumber = string(make([]byte, process.GetMaxSizeInBytes()))
	err := process.VerifyLengths(hbmi)

	assert.Nil(t, err)
}

func TestNewMessageProcessor_CreateHeartbeatFromP2PMessage(t *testing.T) {
	t.Parallel()

	hb := data.Heartbeat{
		Payload:         []byte("Payload"),
		Pubkey:          []byte("PubKey"),
		Signature:       []byte("signed"),
		VersionNumber:   "VersionNumber",
		NodeDisplayName: "NodeDisplayName",
	}

	marshalizer := &mock.MarshalizerStub{}

	marshalizer.UnmarshalHandler = func(obj interface{}, buff []byte) error {
		(obj.(*data.Heartbeat)).Pubkey = hb.Pubkey
		(obj.(*data.Heartbeat)).Payload = hb.Payload
		(obj.(*data.Heartbeat)).Signature = hb.Signature
		(obj.(*data.Heartbeat)).VersionNumber = hb.VersionNumber
		(obj.(*data.Heartbeat)).NodeDisplayName = hb.NodeDisplayName

		return nil
	}

	updatePubKeyWasCalled := false
	updatePidCalled := false
	mon, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{Signer: &mock.SinglesignMock{}},
		marshalizer,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updatePubKeyWasCalled = true
			},
			UpdatePeerIDCalled: func(pid core.PeerID) {
				updatePidCalled = true
			},
		},
	)
	assert.Nil(t, err)

	message := &mock.P2PMessageStub{
		FromField:      nil,
		DataField:      make([]byte, 5),
		SeqNoField:     nil,
		TopicField:     "",
		SignatureField: nil,
		KeyField:       nil,
		PeerField:      "",
	}

	ret, err := mon.CreateHeartbeatFromP2PMessage(message)

	assert.Nil(t, err)
	assert.NotNil(t, ret)
	assert.True(t, updatePubKeyWasCalled)
	assert.True(t, updatePidCalled)
}

func TestNewMessageProcessor_CreateHeartbeatFromP2pMessageWithNilDataShouldErr(t *testing.T) {
	t.Parallel()

	message := &mock.P2PMessageStub{
		FromField:      nil,
		DataField:      nil,
		SeqNoField:     nil,
		TopicField:     "",
		SignatureField: nil,
		KeyField:       nil,
		PeerField:      "",
	}

	mon, _ := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		&mock.MarshalizerStub{},
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {},
		},
	)

	ret, err := mon.CreateHeartbeatFromP2PMessage(message)

	assert.Nil(t, ret)
	assert.Equal(t, heartbeat.ErrNilDataToProcess, err)
}

func TestNewMessageProcessor_CreateHeartbeatFromP2pMessageWithUnmarshaliableDataShouldErr(t *testing.T) {
	t.Parallel()

	message := &mock.P2PMessageStub{
		FromField:      nil,
		DataField:      []byte("hello"),
		SeqNoField:     nil,
		TopicField:     "",
		SignatureField: nil,
		KeyField:       nil,
		PeerField:      "",
	}

	expectedErr := errors.New("marshal didn't work")

	mon, _ := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		&mock.MarshalizerStub{
			UnmarshalHandler: func(obj interface{}, buff []byte) error {
				return expectedErr
			},
		},
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {},
		},
	)

	ret, err := mon.CreateHeartbeatFromP2PMessage(message)

	assert.Nil(t, ret)
	assert.Equal(t, expectedErr, err)
}

func TestNewMessageProcessor_CreateHeartbeatFromP2PMessageWithTooLongLengthsShouldErr(t *testing.T) {
	t.Parallel()

	length := 129
	buff := make([]byte, length)

	for i := 0; i < length; i++ {
		buff[i] = byte(97)
	}
	bigNodeName := string(buff)

	hb := data.Heartbeat{
		Payload:         []byte("Payload"),
		Pubkey:          []byte("PubKey"),
		Signature:       []byte("signed"),
		VersionNumber:   "VersionNumber",
		NodeDisplayName: bigNodeName,
	}

	marshalizer := &mock.MarshalizerStub{}

	marshalizer.UnmarshalHandler = func(obj interface{}, buff []byte) error {
		(obj.(*data.Heartbeat)).Pubkey = hb.Pubkey
		(obj.(*data.Heartbeat)).Payload = hb.Payload
		(obj.(*data.Heartbeat)).Signature = hb.Signature
		(obj.(*data.Heartbeat)).VersionNumber = hb.VersionNumber
		(obj.(*data.Heartbeat)).NodeDisplayName = hb.NodeDisplayName

		return nil
	}

	mon, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		marshalizer,
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {},
		},
	)
	assert.Nil(t, err)

	message := &mock.P2PMessageStub{
		FromField:      nil,
		DataField:      make([]byte, 5),
		SeqNoField:     nil,
		TopicField:     "",
		SignatureField: nil,
		KeyField:       nil,
		PeerField:      "",
	}

	ret, err := mon.CreateHeartbeatFromP2PMessage(message)

	assert.Nil(t, ret)
	assert.True(t, errors.Is(err, heartbeat.ErrPropertyTooLong))
}

func TestNewMessageProcessor_CreateHeartbeatFromP2pNilMessageShouldErr(t *testing.T) {
	t.Parallel()

	mon, _ := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{},
		&mock.MarshalizerStub{},
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {},
		},
	)

	ret, err := mon.CreateHeartbeatFromP2PMessage(nil)

	assert.Nil(t, ret)
	assert.Equal(t, heartbeat.ErrNilMessage, err)
}

func createHeartbeatMarshalizerStub(hb *data.Heartbeat) *mock.MarshalizerStub {
	marshalizer := &mock.MarshalizerStub{}
	marshalizer.UnmarshalHandler = func(obj interface{}, buff []byte) error {
		(obj.(*data.Heartbeat)).Pubkey = hb.Pubkey
		(obj.(*data.Heartbeat)).Payload = hb.Payload
		(obj.(*data.Heartbeat)).Signature = hb.Signature
		(obj.(*data.Heartbeat)).VersionNumber = hb.VersionNumber
		(obj.(*data.Heartbeat)).NodeDisplayName = hb.NodeDisplayName
		(obj.(*data.Heartbeat)).Pid = hb.Pid

		return nil
	}

	return marshalizer
}

func TestNewMessageProcessor_CreateHeartbeatFromP2PMessagePidMismatchShouldErrAndNotUpdateMapper(t *testing.T) {
	t.Parallel()

	validatorPid := core.PeerID("validator pid")
	originatorPid := core.PeerID("originator pid")

	//a heartbeat validly signed by the validator, but relayed on the topic by another peer
	hb := &data.Heartbeat{
		Payload:         []byte("Payload"),
		Pubkey:          []byte("PubKey"),
		Signature:       []byte("signed"),
		VersionNumber:   "VersionNumber",
		NodeDisplayName: "NodeDisplayName",
		Pid:             validatorPid.Bytes(),
	}

	updatePubKeyWasCalled := false
	updatePidCalled := false
	removedPid := core.PeerID("")
	mp, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{Signer: &mock.SinglesignMock{}},
		createHeartbeatMarshalizerStub(hb),
		&mock.NetworkShardingCollectorStub{
			UpdatePeerIDPublicKeyCalled: func(pid core.PeerID, pk []byte) {
				updatePubKeyWasCalled = true
			},
			UpdatePeerIDCalled: func(pid core.PeerID) {
				updatePidCalled = true
			},
			RemovePeerIDAssociationCalled: func(pid core.PeerID) {
				removedPid = pid
			},
		},
	)
	assert.Nil(t, err)

	message := &mock.P2PMessageStub{
		DataField: make([]byte, 5),
		PeerField: originatorPid,
	}

	ret, err := mp.CreateHeartbeatFromP2PMessage(message)

	assert.Nil(t, ret)
	assert.True(t, errors.Is(err, heartbeat.ErrHeartbeatPidMismatch))
	assert.False(t, updatePubKeyWasCalled)
	assert.False(t, updatePidCalled)
	assert.Equal(t, originatorPid, removedPid)
}

func TestNewMessageProcessor_CreateHeartbeatFromP2PMessageReplayedHeartbeatShouldNotMakeOriginatorAValidator(t *testing.T) {
	t.Parallel()

	validatorPid := core.PeerID("validator pid")
	originatorPid := core.PeerID("originator pid")
	validatorPk := []byte("PubKey")

	psm, err := networksharding.NewPeerShardMapper(
		testscommon.NewCacherMock(),
		testscommon.NewCacherMock(),
		&networksharding.NodesCoordinatorStub{
			GetValidatorWithPublicKeyCalled: func(publicKey []byte) (sharding.Validator, error) {
				if bytes.Equal(publicKey, validatorPk) {
					return nil, nil
				}

				return nil, errors.New("not a validator")
			},
		},
		0,
	)
	assert.Nil(t, err)

	hb := &data.Heartbeat{
		Payload:         []byte("Payload"),
		Pubkey:          validatorPk,
		Signature:       []byte("signed"),
		VersionNumber:   "VersionNumber",
		NodeDisplayName: "NodeDisplayName",
		Pid:             validatorPid.Bytes(),
	}

	mp, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{Signer: &mock.SinglesignMock{}},
		createHeartbeatMarshalizerStub(hb),
		psm,
	)
	assert.Nil(t, err)

	//the attacker replays the validator's heartbeat under its own pubsub identity
	ret, err := mp.CreateHeartbeatFromP2PMessage(&mock.P2PMessageStub{
		DataField: make([]byte, 5),
		PeerField: originatorPid,
	})

	assert.Nil(t, ret)
	assert.True(t, errors.Is(err, heartbeat.ErrHeartbeatPidMismatch))
	assert.NotEqual(t, core.ValidatorPeer, psm.GetPeerInfo(originatorPid).PeerType)

	//sanity check: the very same heartbeat sent by its owner does bind the pid to the validator public key
	ret, err = mp.CreateHeartbeatFromP2PMessage(&mock.P2PMessageStub{
		DataField: make([]byte, 5),
		PeerField: validatorPid,
	})

	assert.Nil(t, err)
	assert.NotNil(t, ret)
	assert.Equal(t, core.ValidatorPeer, psm.GetPeerInfo(validatorPid).PeerType)
}

func TestNewMessageProcessor_CreateHeartbeatFromP2PMessagePidMismatchShouldRemoveExistingAssociation(t *testing.T) {
	t.Parallel()

	validatorPid := core.PeerID("validator pid")
	originatorPid := core.PeerID("originator pid")
	validatorPk := []byte("PubKey")

	psm, err := networksharding.NewPeerShardMapper(
		testscommon.NewCacherMock(),
		testscommon.NewCacherMock(),
		&networksharding.NodesCoordinatorStub{
			GetValidatorWithPublicKeyCalled: func(publicKey []byte) (sharding.Validator, error) {
				return nil, nil
			},
		},
		0,
	)
	assert.Nil(t, err)

	//simulate an association learned before this node was upgraded
	psm.UpdatePeerIDPublicKey(originatorPid, validatorPk)
	assert.Equal(t, core.ValidatorPeer, psm.GetPeerInfo(originatorPid).PeerType)

	hb := &data.Heartbeat{
		Payload:         []byte("Payload"),
		Pubkey:          validatorPk,
		Signature:       []byte("signed"),
		VersionNumber:   "VersionNumber",
		NodeDisplayName: "NodeDisplayName",
		Pid:             validatorPid.Bytes(),
	}

	mp, err := process.NewMessageProcessor(
		&mock.PeerSignatureHandler{Signer: &mock.SinglesignMock{}},
		createHeartbeatMarshalizerStub(hb),
		psm,
	)
	assert.Nil(t, err)

	_, err = mp.CreateHeartbeatFromP2PMessage(&mock.P2PMessageStub{
		DataField: make([]byte, 5),
		PeerField: originatorPid,
	})

	assert.True(t, errors.Is(err, heartbeat.ErrHeartbeatPidMismatch))
	assert.Equal(t, core.UnknownPeer, psm.GetPeerInfo(originatorPid).PeerType)
}
