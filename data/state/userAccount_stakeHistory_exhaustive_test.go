package state_test

import (
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/require"
)

type exhOp struct {
	freeze bool
	epoch  uint32
	amount int64
}

// exhRef mirrors the BASE bucket construction only -- it knows nothing about History.
type exhRef struct {
	exists   bool
	staked   uint32
	value    int64
	unstaked uint32
}

func (r *exhRef) apply(o exhOp) {
	if o.freeze {
		if !r.exists {
			r.exists, r.value = true, 0
		}
		r.value += o.amount
		r.staked = o.epoch
		r.unstaked = core.DefaultUnstakedEpoch
		return
	}
	if r.exists && r.unstaked == core.DefaultUnstakedEpoch {
		r.unstaked = o.epoch
	}
}

// stake live during epoch e = replay every op that happened strictly before e began
func exhRefStakeAt(ops []exhOp, e uint32) int64 {
	var r exhRef
	for _, o := range ops {
		if o.epoch < e {
			r.apply(o)
		}
	}
	if r.exists && r.unstaked == core.DefaultUnstakedEpoch && r.staked < e {
		return r.value
	}
	return 0
}

func TestUserAccount_StakeHistory_ExhaustiveClaimPath(t *testing.T) {
	fc := &mock.ForkControllerStub{
		FixAuditChangesV3Value: true,
		FixAuditChangesV5Value: true,
		FixStakingBucketsValue: true,
		BigBucketsComputeValue: true,
		ClaimKFIValue:          true,
	}
	assetID := []byte("EXH-1234")
	key := string(assetID)
	const pool = int64(1_000_000)
	const maxEpoch = uint32(5)
	const maxLen = 4

	var choices []exhOp
	for e := uint32(1); e <= maxEpoch; e++ {
		choices = append(choices, exhOp{freeze: true, epoch: e, amount: 40})
		choices = append(choices, exhOp{freeze: true, epoch: e, amount: 60})
		choices = append(choices, exhOp{epoch: e})
	}

	checked, seqs, mismatch := 0, 0, 0
	var seq []exhOp

	var walk func(depth int, minEpoch uint32)
	walk = func(depth int, minEpoch uint32) {
		if len(seq) > 0 {
			seqs++
			acc, _ := state.NewUserAccount([]byte("addr"))
			userKDA := &kapps.UserKDA{
				LastClaim: &kapps.LastClaim{},
				Buckets:   make(map[string]*kapps.UserBucket),
			}
			staking := &kapps.StakingData{}
			var applied []exhOp
			for _, o := range seq {
				// Predict success from the reference model, independently of the code. Without
				// this a regression that rejected valid operations would quietly shrink the
				// space being explored while the test stayed green.
				var model exhRef
				for _, prev := range applied {
					model.apply(prev)
				}

				if o.freeze {
					err := acc.Freeze(assetID, nil, o.amount, staking, userKDA, state.FreezeOptions{
						BlockEpoch: o.epoch, BlockTime: int64(o.epoch) * 1000,
						NewStakingFlow: true, KeepStakeHistory: true,
					})
					require.NoError(t, err, "a freeze of a positive amount must always succeed")
					applied = append(applied, o)
					continue
				}

				// MinEpochsToUnstake is zero here, so an unfreeze succeeds exactly when the
				// bucket exists and is still active.
				wantOK := model.exists && model.unstaked == core.DefaultUnstakedEpoch
				_, _, err := acc.Unfreeze(assetID, nil, o.epoch, staking, userKDA, true)
				require.Equal(t, wantOK, err == nil, "unfreeze acceptance at epoch %d", o.epoch)
				if err == nil {
					applied = append(applied, o)
				}
			}

			if userKDA.Buckets[key] != nil {
				for e := uint32(1); e <= maxEpoch+1; e++ {
					want := exhRefStakeAt(applied, e)

					single := &kapps.StakingData{
						InterestType:     kapps.StakingData_FPRI,
						MinEpochsToClaim: 0,
						FPR: []*kapps.FPRData{{
							Epoch: e, TotalAmount: pool, TotalStaked: pool,
						}},
					}
					gains, err := acc.ComputeAvailableClaim(assetID, e, int64(e)*1000, userKDA, single, fc)
					if err != nil {
						t.Fatalf("seq=%v epoch=%d: %v", applied, e, err)
					}
					got := gains[string(kdautils.KLVIdentifier)]

					checked++
					if got != want {
						mismatch++
						if mismatch <= 8 {
							b := userKDA.Buckets[key]
							t.Errorf("seq=%v fprEpoch=%d: got %d want %d (staked=%d val=%d unstaked=%d hist=%v)",
								applied, e, got, want, b.StakedEpoch, b.Value, b.UnstakedEpoch, b.History)
						}
					}
				}
			}
		}
		if depth == 0 {
			return
		}
		for _, c := range choices {
			if c.epoch < minEpoch {
				continue
			}
			seq = append(seq, c)
			walk(depth-1, c.epoch)
			seq = seq[:len(seq)-1]
		}
	}

	walk(maxLen, 1)
	t.Logf("%d sequences, %d epoch-comparisons, %d mismatches", seqs, checked, mismatch)
}
