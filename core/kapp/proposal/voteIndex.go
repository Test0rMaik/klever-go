package proposal

import (
	"encoding/binary"
	"math"
	"slices"

	"github.com/klever-io/klever-go/common"
	"github.com/klever-io/klever-go/core/kapp"
	"github.com/klever-io/klever-go/core/process/kda/kdautils"
	"github.com/klever-io/klever-go/kapps"
)

// An index entry is the proposal id followed by the voted amount, both big-endian uint64, so a
// stored index is a multiple of 16 bytes and MaxLeafSize caps it at 49152 entries.
const (
	proposalIDSize     = 8
	voteAmountSize     = 8
	voteIndexEntrySize = proposalIDSize + voteAmountSize
)

func decodeProposalVoteIndex(raw []byte) ([]kapp.ProposalVoteIndexEntry, error) {
	if len(raw)%voteIndexEntrySize != 0 {
		return nil, common.ErrInvalidValue
	}

	entries := make([]kapp.ProposalVoteIndexEntry, 0, len(raw)/voteIndexEntrySize)
	for offset := 0; offset < len(raw); offset += voteIndexEntrySize {
		amount := binary.BigEndian.Uint64(raw[offset+proposalIDSize : offset+voteIndexEntrySize])
		if amount == 0 || amount > math.MaxInt64 {
			// A vote is validated positive on the way in and a subtraction never leaves it
			// below one, so a stored zero, or an amount the signed type cannot hold, is
			// corruption. Fail closed: either would read as covered by any balance and never
			// be shrunk again.
			return nil, common.ErrInvalidValue
		}

		entries = append(entries, kapp.ProposalVoteIndexEntry{
			ProposalID: binary.BigEndian.Uint64(raw[offset : offset+proposalIDSize]),
			Amount:     int64(amount), // #nosec G115 -- bounded to MaxInt64 above
		})
	}

	return entries, nil
}

func encodeProposalVoteIndex(entries []kapp.ProposalVoteIndexEntry) ([]byte, error) {
	raw := make([]byte, 0, len(entries)*voteIndexEntrySize)
	for _, entry := range entries {
		if entry.Amount <= 0 {
			// Refuse to store what decode would refuse; see there.
			return nil, common.ErrInvalidValue
		}

		raw = binary.BigEndian.AppendUint64(raw, entry.ProposalID)
		raw = binary.BigEndian.AppendUint64(raw, uint64(entry.Amount)) // #nosec G115 -- rejected non-positive above
	}

	return raw, nil
}

// upsertProposalVoteIndex records amount as the vote on proposalID: a new entry for a first vote,
// the existing entry updated for a re-vote, which replaces the amount rather than adding to it.
// It reports whether anything changed, so an unchanged index is not rewritten.
func upsertProposalVoteIndex(
	entries []kapp.ProposalVoteIndexEntry,
	proposalID uint64,
	amount int64,
) ([]kapp.ProposalVoteIndexEntry, bool) {
	i := slices.IndexFunc(entries, func(entry kapp.ProposalVoteIndexEntry) bool { return entry.ProposalID == proposalID })
	if i < 0 {
		return append(entries, kapp.ProposalVoteIndexEntry{ProposalID: proposalID, Amount: amount}), true
	}
	if entries[i].Amount == amount {
		return entries, false
	}

	entries[i].Amount = amount

	return entries, true
}

// dropInactiveProposalVotes removes every entry whose proposal is in none of the controller's
// buckets: a settled proposal, or one that never existed, whose entry no unfreeze needs. It
// reports whether anything was dropped.
func dropInactiveProposalVotes(
	entries []kapp.ProposalVoteIndexEntry,
	controller *kapps.ProposalController,
) ([]kapp.ProposalVoteIndexEntry, bool) {
	active := kdautils.ActiveProposalIDs(controller)

	before := len(entries)
	entries = slices.DeleteFunc(entries, func(entry kapp.ProposalVoteIndexEntry) bool {
		_, ok := active[entry.ProposalID]
		return !ok
	})

	return entries, len(entries) != before
}
