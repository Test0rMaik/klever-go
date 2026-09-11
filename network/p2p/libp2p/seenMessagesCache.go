package libp2p

import (
	"container/list"
	"sync"
	"time"
)

// seenMessagesCache is the direct-send replay filter: a fixed-size FIFO of (seqno, timestamp) per
// peer, and an LRU of peers. A peer's identifiers are dropped only by that peer's own traffic (its
// FIFO is full) or by time (they expired) — nothing one peer sends can shorten another's replay
// window, so the window is min(span, perPeer identifiers) and the peer alone decides which.
//
// The peer count is bounded too. When maxPeers buckets all hold live identifiers a new peer is
// not tracked until the least recently active one has been quiet for span: its frames are
// processed without dedup. That is the single fail-open here, chosen over evicting a live bucket
// because it costs the newcomer its own replay protection rather than someone else's. It is not
// silent: the refusal is logged, bounded to once per span, so an operator can see the table is
// full rather than infer it from duplicated work.
type seenMessagesCache struct {
	mut      sync.Mutex
	span     time.Duration
	perPeer  int
	maxPeers int
	peers    map[string]*list.Element // *peerSeen
	byTouch  *list.List               // front is the most recently touched peer
	// lastRefusedWarn bounds the fail-open warning to one line per span. A refused newcomer is
	// peer-triggerable — rotating identities is exactly how the table gets filled — so logging
	// each refusal would hand a peer the node's log volume. One line per span is enough: the
	// condition it reports lasts at least that long by construction.
	lastRefusedWarn time.Time
}

// peerSeen keeps seen in insertion order, which is also timestamp order: the clock is read under
// the lock that appends. Lookups walk it newest-first and stop at the first expired entry, so
// expired identifiers are never consulted and do not need collecting — the FIFO drops them.
type peerSeen struct {
	id      string
	seen    []seenEntry
	touched time.Time
}

type seenEntry struct {
	seqno uint64
	at    time.Time
}

// newSeenMessagesCache clamps non-positive bounds to the built-in defaults rather than treating
// them as "unlimited": these values are reachable from configuration, where an unset field is
// zero, and an unbounded cache is the thing this type exists to prevent.
func newSeenMessagesCache(span time.Duration, maxEntries int, perPeer int) *seenMessagesCache {
	if maxEntries <= 0 {
		maxEntries = defaultMaxSeenMessages
	}
	if perPeer <= 0 {
		perPeer = seenMessagesPerPeer
	}
	maxPeers := maxEntries / perPeer
	if maxPeers < 1 {
		maxPeers = 1
	}

	return &seenMessagesCache{
		span:     span,
		perPeer:  perPeer,
		maxPeers: maxPeers,
		peers:    make(map[string]*list.Element),
		byTouch:  list.New(),
	}
}

// refusalWarning is the decision to warn about a refused newcomer, taken under the lock and
// acted on outside it. The logger writes to every sink synchronously — stdout, log files — so a
// stalled sink under smc.mut would stall dedup for every peer, not just delay one line.
type refusalWarning struct {
	fire    bool
	tracked int
}

func (smc *seenMessagesCache) hasOrAdd(peerID string, seqno uint64) bool {
	seen, warning := smc.hasOrAddLocked(peerID, seqno)
	if warning.fire {
		log.Warn("direct-send replay cache full: new peer not deduplicated",
			"trackedPeers", warning.tracked, "maxPeers", smc.maxPeers, "span", smc.span)
	}

	return seen
}

func (smc *seenMessagesCache) hasOrAddLocked(peerID string, seqno uint64) (bool, refusalWarning) {
	smc.mut.Lock()
	defer smc.mut.Unlock()

	now := time.Now()

	el := smc.peers[peerID]
	if el == nil {
		if len(smc.peers) >= smc.maxPeers && !smc.dropIdlest(now) {
			// Processed without dedup — the one fail-open here. Silent, it is indistinguishable
			// from replay protection working; the yaml tells operators to raise the cap when this
			// fires, which they cannot do if it never surfaces.
			var warning refusalWarning
			if now.Sub(smc.lastRefusedWarn) >= smc.span {
				smc.lastRefusedWarn = now
				warning = refusalWarning{fire: true, tracked: len(smc.peers)}
			}

			return false, warning
		}

		el = smc.byTouch.PushFront(&peerSeen{id: peerID, seen: make([]seenEntry, 0, smc.perPeer)})
		smc.peers[peerID] = el
	}

	bucket := el.Value.(*peerSeen)
	bucket.touched = now
	smc.byTouch.MoveToFront(el)

	if bucket.holds(seqno, now, smc.span) {
		return true, refusalWarning{}
	}

	// Full: drop the oldest in place. The slice was sized once at perPeer and never regrows.
	if len(bucket.seen) == smc.perPeer {
		copy(bucket.seen, bucket.seen[1:])
		bucket.seen = bucket.seen[:smc.perPeer-1]
	}
	bucket.seen = append(bucket.seen, seenEntry{seqno: seqno, at: now})

	return false, refusalWarning{}
}

// dropIdlest frees one bucket slot if the least recently touched peer has been quiet for span,
// which means every identifier it holds has expired. The LRU makes that an O(1) check: no other
// bucket can be idle longer than the one at the back.
func (smc *seenMessagesCache) dropIdlest(now time.Time) bool {
	back := smc.byTouch.Back()
	if back == nil {
		return false
	}

	bucket := back.Value.(*peerSeen)
	if now.Sub(bucket.touched) < smc.span {
		return false
	}

	smc.byTouch.Remove(back)
	delete(smc.peers, bucket.id)

	return true
}

func (ps *peerSeen) holds(seqno uint64, now time.Time, span time.Duration) bool {
	for i := len(ps.seen) - 1; i >= 0; i-- {
		if now.Sub(ps.seen[i].at) >= span {
			return false
		}
		if ps.seen[i].seqno == seqno {
			return true
		}
	}

	return false
}

func (ps *peerSeen) live(now time.Time, span time.Duration) int {
	n := 0
	for i := len(ps.seen) - 1; i >= 0 && now.Sub(ps.seen[i].at) < span; i-- {
		n++
	}

	return n
}

// has and len are read-only views for tests. They apply the same expiry rule as hasOrAdd, so an
// expired entry is not "seen" whichever of the three is asked.
func (smc *seenMessagesCache) has(peerID string, seqno uint64) bool {
	smc.mut.Lock()
	defer smc.mut.Unlock()

	el := smc.peers[peerID]
	if el == nil {
		return false
	}

	return el.Value.(*peerSeen).holds(seqno, time.Now(), smc.span)
}

func (smc *seenMessagesCache) len() int {
	smc.mut.Lock()
	defer smc.mut.Unlock()

	now := time.Now()
	n := 0
	for _, el := range smc.peers {
		n += el.Value.(*peerSeen).live(now, smc.span)
	}

	return n
}
