package state_test

import (
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/require"
)

// A pool deposited while nobody was staked has TotalStaked == 0. Recording only staked
// intervals keeps such a pool unreachable: the gap epoch falls outside every segment, so
// bucketStakeAt reports "not staked" and accrueBucketFPR returns before the arithmetic.
//
// This matters because calculateFPRAmount's pre-BigBucketsCompute branch would evaluate
// float64(total)/float64(0)*float64(0) -- NaN -- and converting NaN to int64 is
// implementation-dependent in Go: 0 on arm64, MinInt64 on amd64. An encoding that marked the
// gap with a zero-valued segment would report "staked with nothing" and walk straight into it.
func TestUserAccount_StakeHistory_GapPoolIsNotStaked(t *testing.T) {
	assetID := []byte("EXH-1234")

	for name, bigBuckets := range map[string]bool{
		"BigBucketsCompute on":  true,
		"BigBucketsCompute off": false,
	} {
		t.Run(name, func(t *testing.T) {
			fc := &mock.ForkControllerStub{
				FixAuditChangesV3Value: true,
				FixAuditChangesV5Value: true,
				FixStakingBucketsValue: true,
				BigBucketsComputeValue: bigBuckets,
				ClaimKFIValue:          true,
			}
			acc, _ := state.NewUserAccount([]byte("addr"))
			userKDA := &kapps.UserKDA{
				LastClaim: &kapps.LastClaim{},
				Buckets:   make(map[string]*kapps.UserBucket),
			}
			staking := &kapps.StakingData{}
			opts := func(e uint32) state.FreezeOptions {
				return state.FreezeOptions{BlockEpoch: e, BlockTime: int64(e) * 1000,
					NewStakingFlow: true, KeepStakeHistory: true}
			}

			require.NoError(t, acc.Freeze(assetID, nil, 100, staking, userKDA, opts(1)))
			_, _, err := acc.Unfreeze(assetID, nil, 2, staking, userKDA, true)
			require.NoError(t, err)
			require.NoError(t, acc.Freeze(assetID, nil, 50, staking, userKDA, opts(5)))

			// One staked interval, (1,2]. Epochs 3 and 4 are the gap and are simply absent.
			history := userKDA.Buckets[string(assetID)].History
			require.Len(t, history, 1)
			require.Equal(t, uint32(1), history[0].StakedEpoch)
			require.Equal(t, uint32(2), history[0].ThroughEpoch)

			// pool deposited at epoch 3, while total stake was zero
			single := &kapps.StakingData{
				InterestType: kapps.StakingData_FPRI,
				FPR:          []*kapps.FPRData{{Epoch: 3, TotalAmount: 1000, TotalStaked: 0}},
			}
			gains, err := acc.ComputeAvailableClaim(assetID, 5, 5000, userKDA, single, fc)
			require.NoError(t, err)

			// The pool must contribute nothing at all -- not even a zero entry, which would
			// mean the arithmetic ran.
			_, credited := gains[string(kdautils.KLVIdentifier)]
			require.False(t, credited, "gap pool must not reach calculateFPRAmount")
			require.Zero(t, single.FPR[0].TotalClaimed)
		})
	}
}
