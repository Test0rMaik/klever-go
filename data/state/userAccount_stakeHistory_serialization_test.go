package state_test

import (
	"encoding/hex"
	"testing"

	"github.com/klever-io/klever-go/core"
	"github.com/klever-io/klever-go/kapps"
	"github.com/klever-io/klever-go/tools/marshal"
	"github.com/stretchr/testify/require"
)

// UserBucket is marshalled straight into the account data trie (userAccount.go SetUserKDA), so
// its encoding is consensus state. These two assertions are the contract:
//
//  1. a bucket with no history must encode exactly as it did before History existed, or every
//     untouched account on chain changes its state root at the fork
//  2. a populated history must survive a round trip with every field intact
func TestUserAccount_StakeHistory_SerializationContract(t *testing.T) {
	m := marshal.NewProtoMarshalizer()

	t.Run("a bucket with no history encodes to the pre-change bytes", func(t *testing.T) {
		// Frozen golden. Fields 1-5 keep their numbers; History is field 6 and proto3 omits an
		// empty repeated field. Renumbering an existing field would still round-trip against
		// the regenerated code but would break every bucket already on chain -- this catches it.
		const golden = "0880e2cfaa06100718ffffffff0f208827"

		for _, tc := range []struct {
			name    string
			history []*kapps.StakeSegment
		}{
			{"nil history", nil},
			{"empty history", []*kapps.StakeSegment{}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				bucket := &kapps.UserBucket{
					StakedAt: 1700000000, StakedEpoch: 7,
					UnstakedEpoch: core.DefaultUnstakedEpoch, Value: 5000,
					History: tc.history,
				}
				encoded, err := m.Marshal(bucket)
				require.NoError(t, err)
				require.Equal(t, golden, hex.EncodeToString(encoded))
			})
		}
	})

	t.Run("a populated history round trips with every field intact", func(t *testing.T) {
		// Two staked intervals separated by an unstaked gap (5,9] -- the gap is represented by
		// absence, so the decoded list must have exactly these two entries and no filler.
		original := &kapps.UserBucket{
			StakedAt: 1700000000, StakedEpoch: 9,
			UnstakedEpoch: core.DefaultUnstakedEpoch, Value: 5000,
			History: []*kapps.StakeSegment{
				{StakedEpoch: 1, ThroughEpoch: 3, Value: 1000},
				{StakedEpoch: 3, ThroughEpoch: 5, Value: 2500},
			},
		}

		encoded, err := m.Marshal(original)
		require.NoError(t, err)

		decoded := &kapps.UserBucket{}
		require.NoError(t, m.Unmarshal(decoded, encoded))

		require.Equal(t, original.StakedAt, decoded.StakedAt)
		require.Equal(t, original.StakedEpoch, decoded.StakedEpoch)
		require.Equal(t, original.UnstakedEpoch, decoded.UnstakedEpoch)
		require.Equal(t, original.Value, decoded.Value)
		require.Len(t, decoded.History, 2)
		for i, want := range original.History {
			require.Equal(t, want.StakedEpoch, decoded.History[i].StakedEpoch, "segment %d start", i)
			require.Equal(t, want.ThroughEpoch, decoded.History[i].ThroughEpoch, "segment %d end", i)
			require.Equal(t, want.Value, decoded.History[i].Value, "segment %d value", i)
		}

		// Deterministic marshalling: the same logical bucket must produce the same bytes, or
		// two honest nodes disagree on the state root.
		again, err := m.Marshal(decoded)
		require.NoError(t, err)
		require.Equal(t, encoded, again)
	})

	t.Run("bytes written before History decode with an empty history", func(t *testing.T) {
		legacy, err := hex.DecodeString("0880e2cfaa06100718ffffffff0f208827")
		require.NoError(t, err)

		decoded := &kapps.UserBucket{}
		require.NoError(t, m.Unmarshal(decoded, legacy))
		require.Empty(t, decoded.History)
		require.Equal(t, int64(5000), decoded.Value)
		require.Equal(t, uint32(7), decoded.StakedEpoch)
	})
}
