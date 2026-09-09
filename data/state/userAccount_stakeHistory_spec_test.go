package state_test

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/klever-io/klever-go/common/mock"
	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/data/state"
	"github.com/klever-io/klever-go/kapps"
	"github.com/stretchr/testify/require"
)

// Expectations below are derived from the staking rules, not from the implementation:
//
//	R1  a pool for epoch E is shared among stakes LIVE during E
//	R2  live during E = staked before E began and not unstaked before E began
//	R3  a freeze at S makes the stake live from S+1; pool S is not earned
//	R4  an unfreeze at U leaves the stake live THROUGH U; U is earned, nothing after
//	R5  the share is proportional to the stake live that epoch
//	R6  a top-up must not retroactively raise the share of an already-earned pool
//	R7  a top-up must not forfeit an already-earned pool
//	R8  a claim pays every unclaimed pool the staker is entitled to, then advances the cursor
//	R9  a claim is allowed only when blockEpoch-LastClaim >= MinEpochsToClaim
//	R10 freeze and unfreeze attempt a claim first, tolerating a closed window
//	R11 re-freezing an unstaked bucket reactivates the retained principal PLUS the new amount,
//	    and the interval that principal earned closes at the unfreeze epoch (R4)
//
// Every pool has TotalAmount == TotalStaked, so a payout equals the stake priced.
const specPool = int64(1_000_000)

type specStep struct {
	epoch    uint32
	op       string // freeze | unfreeze | claim | withdraw
	amount   int64
	bucketID []byte

	wantRejected bool   // the claim (or pre-claim) is refused by R9
	wantPaid     int64  // total paid by this step
	wantHistory  string // history after the step; "" means none
	wantBuckets  int    // -1 to skip
}

func runSpec(t *testing.T, name string, assetID []byte, minClaim, minUnstake, minWithdraw uint32, steps []specStep) {
	t.Run(name, func(t *testing.T) {
		fc := &mock.ForkControllerStub{
			FixAuditChangesV3Value: true, FixAuditChangesV5Value: true,
			FixStakingBucketsValue: true, BigBucketsComputeValue: true, ClaimKFIValue: true,
		}
		acc, _ := state.NewUserAccount([]byte("addr"))
		userKDA := &kapps.UserKDA{LastClaim: &kapps.LastClaim{}, Buckets: map[string]*kapps.UserBucket{}}
		staking := &kapps.StakingData{
			InterestType: kapps.StakingData_FPRI, MinEpochsToClaim: minClaim,
			MinEpochsToUnstake: minUnstake, MinEpochsToWithdraw: minWithdraw,
		}
		for e := uint32(1); e <= 20; e++ {
			staking.FPR = append(staking.FPR, &kapps.FPRData{Epoch: e, TotalAmount: specPool, TotalStaked: specPool})
		}

		tryClaim := func(epoch uint32) (bool, int64) {
			gains, err := acc.ComputeAvailableClaim(assetID, epoch, int64(epoch)*1000, userKDA, staking, fc)
			if err != nil {
				require.True(t, errors.Is(err, state.ErrClaimNotAvailable), "unexpected error: %v", err)
				return false, 0
			}
			userKDA.LastClaim.Epoch = epoch // R8, mirroring accounts.go
			return true, gains[string(kdautils.KLVIdentifier)]
		}

		histOf := func(b *kapps.UserBucket) string {
			if b == nil || len(b.History) == 0 {
				return ""
			}
			parts := make([]string, 0, len(b.History))
			for _, s := range b.History {
				parts = append(parts, fmt.Sprintf("(%d,%d]=%d", s.StakedEpoch, s.ThroughEpoch, s.Value))
			}
			return strings.Join(parts, " ")
		}

		for i, s := range steps {
			label := fmt.Sprintf("step %d (%s @ e%d)", i, s.op, s.epoch)

			var ok bool
			var paid int64
			switch s.op {
			case "claim":
				ok, paid = tryClaim(s.epoch)
			case "freeze":
				ok, paid = tryClaim(s.epoch) // R10
				require.NoError(t, acc.Freeze(assetID, s.bucketID, s.amount, staking, userKDA,
					state.FreezeOptions{BlockEpoch: s.epoch, BlockTime: int64(s.epoch) * 1000,
						NewStakingFlow: true, KeepStakeHistory: true}), label)
			case "unfreeze":
				ok, paid = tryClaim(s.epoch) // R10
				_, _, err := acc.Unfreeze(assetID, s.bucketID, s.epoch, staking, userKDA, true)
				require.NoError(t, err, label)
			case "withdraw":
				ok = true
				_, err := acc.Withdraw(assetID, s.epoch, minWithdraw, userKDA)
				require.NoError(t, err, label)
			}

			require.Equal(t, !s.wantRejected, ok, "%s: claim allowed?", label)
			require.Equal(t, s.wantPaid, paid, "%s: amount paid", label)

			if s.wantBuckets >= 0 {
				require.Len(t, userKDA.Buckets, s.wantBuckets, "%s: bucket count", label)
			}
			if s.op != "withdraw" {
				key := string(assetID)
				if len(s.bucketID) > 0 {
					key = fmt.Sprintf("%x", s.bucketID)
				}
				require.Equal(t, s.wantHistory, histOf(userKDA.Buckets[key]), "%s: history", label)
			}
		}
	})
}

func TestUserAccount_StakeHistory_Spec(t *testing.T) {
	kda := []byte("DEMO-1234")

	// S1 -- stake once, claim on cadence. R3: pool e10 is never earned.
	runSpec(t, "S1 stake once then claim on cadence", kda, 2, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 11, op: "claim", wantRejected: true, wantBuckets: 1},
		{epoch: 12, op: "claim", wantPaid: 2000, wantBuckets: 1}, // e11 + e12
		{epoch: 13, op: "claim", wantRejected: true, wantBuckets: 1},
		{epoch: 14, op: "claim", wantPaid: 2000, wantBuckets: 1}, // e13 + e14
	})

	// S2 -- window always open: R7 is satisfied by the pre-claim, so nothing needs recording.
	runSpec(t, "S2 top-up with the window open records no history", kda, 1, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 11, op: "freeze", amount: 1000, wantPaid: 1000, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "claim", wantPaid: 2000, wantBuckets: 1},
	})

	// S3 -- window shut: history must carry R6 and R7.
	runSpec(t, "S3 top-up with the window shut preserves the earlier stake", kda, 5, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "freeze", amount: 1000, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
		{epoch: 15, op: "claim", wantPaid: 8000, wantHistory: "(10,12]=1000", wantBuckets: 1},
	})

	// S4 -- retirement happens on the NEXT freeze, not at claim time: S3 shows history still
	// present immediately after a claim. Here the e13 pre-claim covers (10,11], and the freeze
	// that follows it drops the segment and records nothing new.
	runSpec(t, "S4 the freeze after a claim retires the history that claim covered", kda, 2, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 11, op: "freeze", amount: 1000, wantRejected: true, wantHistory: "(10,11]=1000", wantBuckets: 1},
		{epoch: 13, op: "freeze", amount: 1000, wantPaid: 5000, wantHistory: "", wantBuckets: 1},
	})

	// S5 -- R4: the unfreeze epoch is earned, nothing after it is. Note the e14 step asserts the
	// CALCULATOR result: ComputeAvailableClaim returns an empty gain without error. A real claim
	// transaction would reject a zero payout with ErrClaimNotAvailable (accounts.go:1661), which
	// TopUpMultiCurrencyAndNoReplay_PostFix pins at the transaction level.
	runSpec(t, "S5 unfreeze earns through the unfreeze epoch and no further", kda, 2, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "unfreeze", wantPaid: 2000, wantHistory: "", wantBuckets: 1},
		{epoch: 14, op: "claim", wantPaid: 0, wantHistory: "", wantBuckets: 1},
	})

	// S6 -- the gap between unfreeze and re-freeze earns nothing.
	runSpec(t, "S6 re-freeze after a gap pays neither gap epoch", kda, 5, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "unfreeze", wantRejected: true, wantHistory: "", wantBuckets: 1},
		{epoch: 14, op: "freeze", amount: 1000, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
		{epoch: 16, op: "claim", wantPaid: 6000, wantHistory: "(10,12]=1000", wantBuckets: 1},
	})

	// S7 -- repeated top-ups inside one epoch describe one interval, not several.
	runSpec(t, "S7 same-epoch top-ups record one interval", kda, 5, 1, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "freeze", amount: 500, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
		{epoch: 12, op: "freeze", amount: 500, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
		{epoch: 12, op: "freeze", amount: 500, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
	})

	// S8 -- unfreeze and re-freeze inside one epoch is still one interval.
	runSpec(t, "S8 same-epoch unfreeze and re-freeze record one interval", kda, 5, 0, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "unfreeze", wantRejected: true, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "freeze", amount: 1000, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
	})

	// S9 -- KLV keys each freeze to its own bucket, so nothing is ever overwritten and no
	// history can arise. This is what bounds the whole feature to custom assets.
	runSpec(t, "S9 KLV freezes make separate buckets and never record history",
		kdautils.KLVIdentifier, 5, 1, 0, []specStep{
			{epoch: 10, op: "freeze", amount: 1000, bucketID: []byte("bucket-a"), wantPaid: 0, wantHistory: "", wantBuckets: 1},
			{epoch: 12, op: "freeze", amount: 1000, bucketID: []byte("bucket-b"), wantRejected: true, wantHistory: "", wantBuckets: 2},
		})

	// S10 -- withdraw removes the bucket, and the history goes with it.
	runSpec(t, "S10 withdraw clears the bucket and its history", kda, 5, 0, 0, []specStep{
		{epoch: 10, op: "freeze", amount: 1000, wantPaid: 0, wantHistory: "", wantBuckets: 1},
		{epoch: 12, op: "unfreeze", wantRejected: true, wantHistory: "", wantBuckets: 1},
		{epoch: 14, op: "freeze", amount: 1000, wantRejected: true, wantHistory: "(10,12]=1000", wantBuckets: 1},
		// 15-10 = 5 satisfies MinEpochsToClaim, so this pre-claim IS allowed: e11 and e12 from
		// the recorded interval, e15 at the current stake, and nothing for the e13/e14 gap.
		{epoch: 15, op: "unfreeze", wantPaid: 4000, wantHistory: "(10,12]=1000", wantBuckets: 1},
		{epoch: 15, op: "withdraw", wantBuckets: 0},
	})
}

// Genesis never sets KeepStakeHistory, so a genesis freeze must record nothing even when the
// bucket it replaces was staked in an earlier epoch.
func TestUserAccount_StakeHistory_GenesisRecordsNothing(t *testing.T) {
	assetID := []byte("DEMO-1234")
	acc, _ := state.NewUserAccount([]byte("addr"))
	userKDA := &kapps.UserKDA{LastClaim: &kapps.LastClaim{}, Buckets: map[string]*kapps.UserBucket{}}
	staking := &kapps.StakingData{}

	opts := func(e uint32) state.FreezeOptions {
		return state.FreezeOptions{BlockEpoch: e, BlockTime: int64(e) * 1000, NewStakingFlow: true}
	}
	require.NoError(t, acc.Freeze(assetID, nil, 1000, staking, userKDA, opts(1)))
	require.NoError(t, acc.Freeze(assetID, nil, 1000, staking, userKDA, opts(5)))

	b := userKDA.Buckets[string(assetID)]
	require.Empty(t, b.History)
	require.Equal(t, uint32(5), b.StakedEpoch)
	require.Equal(t, uint32(core.DefaultUnstakedEpoch), b.UnstakedEpoch)
}

// priceEachPool reports what the claim path pays for each pool epoch in turn, by running one
// single-pool claim per epoch. TotalAmount == TotalStaked, so each result is the stake priced.
func priceEachPool(t *testing.T, acc interface {
	ComputeAvailableClaim([]byte, uint32, int64, *kapps.UserKDA, *kapps.StakingData, core.ForkController) (map[string]int64, error)
}, assetID []byte, userKDA *kapps.UserKDA, fc core.ForkController, at uint32, epochs []uint32) map[uint32]int64 {
	t.Helper()
	out := map[uint32]int64{}
	for _, e := range epochs {
		single := &kapps.StakingData{
			InterestType: kapps.StakingData_FPRI,
			FPR:          []*kapps.FPRData{{Epoch: e, TotalAmount: specPool, TotalStaked: specPool}},
		}
		gains, err := acc.ComputeAvailableClaim(assetID, at, int64(at)*1000, userKDA, single, fc)
		require.NoError(t, err)
		out[e] = gains[string(kdautils.KLVIdentifier)]
	}
	return out
}

// The live scenario at the fork: V5 activates on a chain already full of buckets. Pools forfeited
// before activation must STAY forfeited -- resurrecting them would pay out of pools whose
// denominator never counted this staker -- while every epoch from the first post-activation
// freeze onward must price correctly.
func TestUserAccount_StakeHistory_ActivationOnAnExistingBucket(t *testing.T) {
	assetID := []byte("DEMO-1234")
	acc, _ := state.NewUserAccount([]byte("addr"))
	userKDA := &kapps.UserKDA{LastClaim: &kapps.LastClaim{}, Buckets: map[string]*kapps.UserBucket{}}
	staking := &kapps.StakingData{}

	freeze := func(epoch uint32, amount int64, keepHistory bool) {
		require.NoError(t, acc.Freeze(assetID, nil, amount, staking, userKDA, state.FreezeOptions{
			BlockEpoch: epoch, BlockTime: int64(epoch) * 1000,
			NewStakingFlow: true, KeepStakeHistory: keepHistory,
		}))
	}

	// before activation: nothing is recorded, and the e11/e12 pools are lost the old way
	freeze(10, 1000, false)
	freeze(12, 1000, false)
	require.Empty(t, userKDA.Buckets[string(assetID)].History)

	// first freeze after activation
	freeze(14, 1000, true)
	bucket := userKDA.Buckets[string(assetID)]
	require.Len(t, bucket.History, 1)
	require.Equal(t, uint32(12), bucket.History[0].StakedEpoch)
	require.Equal(t, uint32(14), bucket.History[0].ThroughEpoch)
	require.Equal(t, int64(2000), bucket.History[0].Value)

	on := &mock.ForkControllerStub{
		FixAuditChangesV3Value: true, FixAuditChangesV5Value: true,
		FixStakingBucketsValue: true, BigBucketsComputeValue: true, ClaimKFIValue: true,
	}
	got := priceEachPool(t, acc, assetID, userKDA, on, 20, []uint32{11, 12, 13, 14, 15})

	// Activation must not reach back past the first post-activation freeze.
	require.Zero(t, got[11], "e11 was forfeited before activation and must stay forfeited")
	require.Zero(t, got[12], "e12 likewise; the bucket was re-staked in e12")
	// From the recorded interval onward, pricing is exact.
	require.Equal(t, int64(2000), got[13])
	require.Equal(t, int64(2000), got[14])
	require.Equal(t, int64(3000), got[15])
}

// A claim can land in the MIDDLE of a recorded interval: the pools at or below the cursor are
// skipped as already paid, but the rest of that same interval must still price from it. The
// exhaustive sweep never advances its cursor, so this boundary is only covered here.
func TestUserAccount_StakeHistory_PricesThroughAPartiallyClaimedSegment(t *testing.T) {
	assetID := []byte("DEMO-1234")
	acc, _ := state.NewUserAccount([]byte("addr"))
	userKDA := &kapps.UserKDA{
		LastClaim: &kapps.LastClaim{Epoch: 6},
		Buckets: map[string]*kapps.UserBucket{
			string(assetID): {
				StakedEpoch: 10, Value: 300, UnstakedEpoch: core.DefaultUnstakedEpoch,
				History: []*kapps.StakeSegment{{StakedEpoch: 4, ThroughEpoch: 8, Value: 100}},
			},
		},
	}

	on := &mock.ForkControllerStub{
		FixAuditChangesV3Value: true, FixAuditChangesV5Value: true,
		FixStakingBucketsValue: true, BigBucketsComputeValue: true, ClaimKFIValue: true,
	}
	got := priceEachPool(t, acc, assetID, userKDA, on, 12, []uint32{5, 6, 7, 8, 9, 10, 11})

	require.Zero(t, got[5], "at or below the cursor, already paid")
	require.Zero(t, got[6], "at or below the cursor, already paid")
	require.Equal(t, int64(100), got[7], "still inside (4,8] and above the cursor")
	require.Equal(t, int64(100), got[8], "the interval's closing epoch")
	require.Zero(t, got[9], "gap between the interval and the current stake")
	require.Zero(t, got[10], "re-staked during e10, so e10 is not earned")
	require.Equal(t, int64(300), got[11], "current stake")
}

// Pruning happens only inside stakeHistoryAfterFreeze, so a freeze is the only thing that
// retires history. Three combinations the existing cases miss: retiring more than one segment
// at once, retiring while the bucket is unstaked (the re-freeze path prunes before it appends),
// and a segment that straddles the cursor surviving alongside them.
func TestUserAccount_StakeHistory_PruneRetiresEverySettledInterval(t *testing.T) {
	assetID := []byte("DEMO-1234")
	acc, _ := state.NewUserAccount([]byte("addr"))
	userKDA := &kapps.UserKDA{
		LastClaim: &kapps.LastClaim{Epoch: 9},
		Buckets: map[string]*kapps.UserBucket{
			string(assetID): {
				StakedEpoch: 10, Value: 400, UnstakedEpoch: 12, // unstaked, about to be re-frozen
				History: []*kapps.StakeSegment{
					{StakedEpoch: 1, ThroughEpoch: 4, Value: 100},  // settled, retire
					{StakedEpoch: 4, ThroughEpoch: 7, Value: 200},  // settled, retire
					{StakedEpoch: 7, ThroughEpoch: 10, Value: 300}, // straddles the cursor, keep
				},
			},
		},
	}

	require.NoError(t, acc.Freeze(assetID, nil, 100, &kapps.StakingData{}, userKDA,
		state.FreezeOptions{BlockEpoch: 15, BlockTime: 15000, NewStakingFlow: true, KeepStakeHistory: true}))

	history := userKDA.Buckets[string(assetID)].History
	require.Len(t, history, 2, "two settled intervals retire, the straddling one survives")

	require.Equal(t, uint32(7), history[0].StakedEpoch)
	require.Equal(t, uint32(10), history[0].ThroughEpoch)
	require.Equal(t, int64(300), history[0].Value)

	// The interval appended for the stake being replaced closes at the UNFREEZE, not at e15.
	require.Equal(t, uint32(10), history[1].StakedEpoch)
	require.Equal(t, uint32(12), history[1].ThroughEpoch)
	require.Equal(t, int64(400), history[1].Value)
}

// The practical growth bound: history is limited by the claim cursor, not by how long the
// account lives. A staker topping up every epoch and claiming every fifth must never accumulate
// more than the unclaimed window, however many epochs pass.
func TestUserAccount_StakeHistory_StaysBoundedByTheClaimCursor(t *testing.T) {
	assetID := []byte("DEMO-1234")
	fc := &mock.ForkControllerStub{
		FixAuditChangesV3Value: true, FixAuditChangesV5Value: true,
		FixStakingBucketsValue: true, BigBucketsComputeValue: true, ClaimKFIValue: true,
	}
	acc, _ := state.NewUserAccount([]byte("addr"))
	userKDA := &kapps.UserKDA{LastClaim: &kapps.LastClaim{}, Buckets: map[string]*kapps.UserBucket{}}
	staking := &kapps.StakingData{InterestType: kapps.StakingData_FPRI, MinEpochsToClaim: 5}
	for e := uint32(1); e <= 205; e++ {
		staking.FPR = append(staking.FPR, &kapps.FPRData{Epoch: e, TotalAmount: specPool, TotalStaked: specPool})
	}

	const claimEvery = 5
	longest := 0

	for epoch := uint32(1); epoch <= 200; epoch++ {
		require.NoError(t, acc.Freeze(assetID, nil, 10, staking, userKDA, state.FreezeOptions{
			BlockEpoch: epoch, BlockTime: int64(epoch) * 1000,
			NewStakingFlow: true, KeepStakeHistory: true,
		}))

		if n := len(userKDA.Buckets[string(assetID)].History); n > longest {
			longest = n
		}

		if epoch%claimEvery == 0 {
			_, err := acc.ComputeAvailableClaim(assetID, epoch, int64(epoch)*1000, userKDA, staking, fc)
			require.NoError(t, err)
			userKDA.LastClaim.Epoch = epoch // as accounts.go does after a successful claim
		}
	}

	// One interval per unclaimed epoch and no more -- 200 epochs of top-ups do not accumulate.
	require.LessOrEqual(t, longest, claimEvery,
		"history must stay within the unclaimed window, got %d after 200 epochs", longest)
	require.LessOrEqual(t, len(userKDA.Buckets[string(assetID)].History), claimEvery)
}

// Pruning happens only on a freeze, so intervals settled by a claim survive until the next one.
// That is a state-size question, not a correctness one: a settled interval ends at or below the
// cursor, and the claim loop skips every pool at or below the cursor, so it can never be matched.
// Proven here by pricing the same bucket with and without the settled intervals present.
func TestUserAccount_StakeHistory_SettledIntervalsAreInert(t *testing.T) {
	assetID := []byte("DEMO-1234")
	on := &mock.ForkControllerStub{
		FixAuditChangesV3Value: true, FixAuditChangesV5Value: true,
		FixStakingBucketsValue: true, BigBucketsComputeValue: true, ClaimKFIValue: true,
	}

	live := []*kapps.StakeSegment{{StakedEpoch: 8, ThroughEpoch: 20, Value: 300}}
	settled := []*kapps.StakeSegment{
		{StakedEpoch: 1, ThroughEpoch: 4, Value: 100}, // ends below the cursor
		{StakedEpoch: 4, ThroughEpoch: 8, Value: 200}, // ends below the cursor
	}

	build := func(history []*kapps.StakeSegment) *kapps.UserKDA {
		return &kapps.UserKDA{
			LastClaim: &kapps.LastClaim{Epoch: 10},
			Buckets: map[string]*kapps.UserBucket{
				string(assetID): {
					StakedEpoch: 20, Value: 500,
					UnstakedEpoch: core.DefaultUnstakedEpoch, History: history,
				},
			},
		}
	}

	acc, _ := state.NewUserAccount([]byte("addr"))
	epochs := []uint32{2, 5, 8, 9, 10, 11, 12, 20, 21}

	carried := priceEachPool(t, acc, assetID, build(append(append([]*kapps.StakeSegment{}, settled...), live...)), on, 25, epochs)
	pruned := priceEachPool(t, acc, assetID, build(live), on, 25, epochs)

	require.Equal(t, pruned, carried,
		"carrying settled intervals must not change a single payout")

	// and the values themselves are the ones the live interval and current stake dictate.
	// Note 9 and 10 sit at or below the cursor: the claim loop skips them before the history is
	// ever consulted, which is the same reason a settled interval can never be reached.
	for _, e := range []uint32{2, 5, 8, 9, 10} {
		require.Zero(t, carried[e], "epoch %d is at or below the cursor, already paid", e)
	}
	require.Equal(t, int64(300), carried[11], "first epoch above the cursor, inside (8,20]")
	require.Equal(t, int64(300), carried[20], "the live interval's closing epoch")
	require.Equal(t, int64(500), carried[21], "current stake")
}

// freeze -> top-up -> unfreeze -> re-freeze. The unfreeze writes no history at all, so the
// question is whether the re-freeze still knows about the interval the top-up recorded.
//
// It does. The prune loop only DROPS settled intervals; everything still above the cursor is
// carried through. So (10,12] survives the re-freeze, and the re-freeze appends (12,14] for the
// stake that was live from the top-up until the unfreeze -- leaving e15/e16 as a genuine gap.
func TestUserAccount_StakeHistory_RefreezeKeepsEarlierIntervals(t *testing.T) {
	assetID := []byte("DEMO-1234")
	acc, _ := state.NewUserAccount([]byte("addr"))
	userKDA := &kapps.UserKDA{LastClaim: &kapps.LastClaim{}, Buckets: map[string]*kapps.UserBucket{}}
	staking := &kapps.StakingData{}

	freeze := func(epoch uint32, amount int64) {
		require.NoError(t, acc.Freeze(assetID, nil, amount, staking, userKDA, state.FreezeOptions{
			BlockEpoch: epoch, BlockTime: int64(epoch) * 1000,
			NewStakingFlow: true, KeepStakeHistory: true,
		}))
	}

	freeze(10, 100)
	freeze(12, 100) // top-up: records (10,12]=100
	require.Len(t, userKDA.Buckets[string(assetID)].History, 1)

	_, _, err := acc.Unfreeze(assetID, nil, 14, staking, userKDA, true)
	require.NoError(t, err)
	require.Len(t, userKDA.Buckets[string(assetID)].History, 1, "unfreeze must not alter history")

	freeze(16, 100) // re-freeze

	history := userKDA.Buckets[string(assetID)].History
	require.Len(t, history, 2, "the earlier interval must survive the re-freeze")

	require.Equal(t, uint32(10), history[0].StakedEpoch)
	require.Equal(t, uint32(12), history[0].ThroughEpoch)
	require.Equal(t, int64(100), history[0].Value)

	require.Equal(t, uint32(12), history[1].StakedEpoch)
	require.Equal(t, uint32(14), history[1].ThroughEpoch, "closes at the unfreeze, not the re-freeze")
	require.Equal(t, int64(200), history[1].Value)

	on := &mock.ForkControllerStub{
		FixAuditChangesV3Value: true, FixAuditChangesV5Value: true,
		FixStakingBucketsValue: true, BigBucketsComputeValue: true, ClaimKFIValue: true,
	}
	got := priceEachPool(t, acc, assetID, userKDA, on, 20, []uint32{11, 12, 13, 14, 15, 16, 17})

	require.Equal(t, int64(100), got[11], "the original 100 was live through the top-up")
	require.Equal(t, int64(100), got[12], "top-up epoch still priced at the old stake")
	require.Equal(t, int64(200), got[13], "after the top-up, before the unfreeze")
	require.Equal(t, int64(200), got[14], "an unfreeze at U still earns pool U")
	require.Zero(t, got[15], "unstaked")
	require.Zero(t, got[16], "unstaked, and re-frozen during e16")
	require.Equal(t, int64(300), got[17], "current stake")
}
