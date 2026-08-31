package mock

import (
	"github.com/klever-io/klever-go/core"
)

// NetworkShardingCollectorStub -
type NetworkShardingCollectorStub struct {
	UpdatePeerIDPublicKeyCalled   func(pid core.PeerID, pk []byte)
	RemovePeerIDAssociationCalled func(pid core.PeerID)
}

// UpdatePeerIDPublicKey -
func (nscs *NetworkShardingCollectorStub) UpdatePeerIDPublicKey(pid core.PeerID, pk []byte) {
	nscs.UpdatePeerIDPublicKeyCalled(pid, pk)
}

// RemovePeerIDAssociation -
func (nscs *NetworkShardingCollectorStub) RemovePeerIDAssociation(pid core.PeerID) {
	if nscs.RemovePeerIDAssociationCalled != nil {
		nscs.RemovePeerIDAssociationCalled(pid)
	}
}

// IsInterfaceNil -
func (nscs *NetworkShardingCollectorStub) IsInterfaceNil() bool {
	return nscs == nil
}
