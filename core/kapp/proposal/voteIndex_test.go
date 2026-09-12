package proposal

import (
	"testing"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func voteEntry(id uint64, amount int64) kapp.ProposalVoteIndexEntry {
	return kapp.ProposalVoteIndexEntry{ProposalID: id, Amount: amount}
}

func TestProposalVoteIndexRoundTrips(t *testing.T) {
	t.Parallel()

	entries := []kapp.ProposalVoteIndexEntry{
		voteEntry(1, 10),
		voteEntry(7, 1),
		voteEntry(4096, 1<<40),
		voteEntry(18446744073709551615, 9223372036854775807),
	}

	raw, err := encodeProposalVoteIndex(entries)
	require.NoError(t, err)

	decoded, err := decodeProposalVoteIndex(raw)
	require.NoError(t, err)
	assert.Equal(t, entries, decoded)
}

// TestProposalVoteIndexFailsClosedOutsideTheAmountRange: a vote amount is positive by validation
// and a subtraction never leaves it below one, so a stored zero or an amount past the signed
// maximum is corruption, and a non-positive amount on the way in is a caller bug. All are refused:
// any of them would read as covered by any balance, a vote the unfreeze would never shrink again.
func TestProposalVoteIndexFailsClosedOutsideTheAmountRange(t *testing.T) {
	t.Parallel()

	tooLarge := []byte{0, 0, 0, 0, 0, 0, 0, 1, 0x80, 0, 0, 0, 0, 0, 0, 0}
	_, err := decodeProposalVoteIndex(tooLarge)
	assert.ErrorIs(t, err, common.ErrInvalidValue, "an amount above MaxInt64 must not decode")

	zero := []byte{0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	_, err = decodeProposalVoteIndex(zero)
	assert.ErrorIs(t, err, common.ErrInvalidValue, "a zero amount must not decode")

	for _, amount := range []int64{-1, 0} {
		_, err = encodeProposalVoteIndex([]kapp.ProposalVoteIndexEntry{voteEntry(1, amount)})
		assert.ErrorIs(t, err, common.ErrInvalidValue, "a non-positive amount must not encode")
	}
}

// TestProposalVoteIndexWireFormat pins the bytes, not only the round trip: a codec that switched
// both sides to little-endian, or swapped the two fields, would still round-trip. This layout is
// consensus state from FixAuditChangesV5 on.
func TestProposalVoteIndexWireFormat(t *testing.T) {
	t.Parallel()

	raw, err := encodeProposalVoteIndex([]kapp.ProposalVoteIndexEntry{
		voteEntry(0x0102030405060708, 0x1112131415161718),
		voteEntry(1, 2),
	})
	require.NoError(t, err)

	assert.Equal(t, []byte{
		0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
		0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 2,
	}, raw)
}

func TestProposalVoteIndexRejectsMisalignedPayload(t *testing.T) {
	t.Parallel()

	_, err := decodeProposalVoteIndex([]byte{1, 2, 3})
	assert.ErrorIs(t, err, common.ErrInvalidValue)

	// The id-only shape from before amounts were stored is not a valid index either.
	_, err = decodeProposalVoteIndex([]byte{0, 0, 0, 0, 0, 0, 0, 1})
	assert.ErrorIs(t, err, common.ErrInvalidValue)
}

func TestProposalVoteIndexDecodesEmptyPayload(t *testing.T) {
	t.Parallel()

	decoded, err := decodeProposalVoteIndex(nil)
	require.NoError(t, err)
	assert.Equal(t, 0, len(decoded))
}

// TestProposalVoteIndexUpsert covers the three outcomes: a first vote appends, the same vote again
// changes nothing, and a re-vote replaces the amount in place — which is what Vote does to the
// proposal's own record, so the index stays equal to it.
func TestProposalVoteIndexUpsert(t *testing.T) {
	t.Parallel()

	entries, changed := upsertProposalVoteIndex(nil, 5, 100)
	assert.True(t, changed)
	assert.Equal(t, []kapp.ProposalVoteIndexEntry{voteEntry(5, 100)}, entries)

	entries, changed = upsertProposalVoteIndex(entries, 5, 100)
	assert.False(t, changed, "the same vote again must not rewrite the index")
	assert.Equal(t, []kapp.ProposalVoteIndexEntry{voteEntry(5, 100)}, entries)

	entries, changed = upsertProposalVoteIndex(entries, 5, 40)
	assert.True(t, changed)
	assert.Equal(t, []kapp.ProposalVoteIndexEntry{voteEntry(5, 40)}, entries, "a re-vote replaces, it does not add")

	entries, changed = upsertProposalVoteIndex(entries, 9, 1)
	assert.True(t, changed)
	assert.Equal(t, []kapp.ProposalVoteIndexEntry{voteEntry(5, 40), voteEntry(9, 1)}, entries, "order is insertion order")
}
