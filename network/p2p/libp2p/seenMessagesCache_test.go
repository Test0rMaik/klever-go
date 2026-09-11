package libp2p

import (
	"fmt"
	"sync"
	"testing"
	"time"

	logger "github.com/klever-io/klever-go-logger"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSeenMessagesCacheDetectsRepeatedSeqnoWithinSpan(t *testing.T) {
	t.Parallel()

	cache := newSeenMessagesCache(time.Minute, 16, 4)

	assert.False(t, cache.hasOrAdd("peer", 1))
	assert.True(t, cache.hasOrAdd("peer", 1))
	assert.Equal(t, 1, cache.len())
}

func TestSeenMessagesCacheSeparatesPeers(t *testing.T) {
	t.Parallel()

	cache := newSeenMessagesCache(time.Minute, 16, 4)

	assert.False(t, cache.hasOrAdd("peer A", 1))
	assert.False(t, cache.hasOrAdd("peer B", 1))
	assert.Equal(t, 2, cache.len())
}

func TestSeenMessagesCacheForgetsSeqnoAfterSpan(t *testing.T) {
	t.Parallel()

	cache := newSeenMessagesCache(time.Millisecond*10, 16, 4)

	assert.False(t, cache.hasOrAdd("peer", 1))
	time.Sleep(time.Millisecond * 30)

	// has must agree with hasOrAdd about an expired entry, otherwise it is not a usable oracle
	assert.False(t, cache.has("peer", 1))
	assert.False(t, cache.hasOrAdd("peer", 1))
	assert.True(t, cache.has("peer", 1))
	assert.Equal(t, 1, cache.len())
}

func TestSeenMessagesCacheEvictsOwnOldestWhenFull(t *testing.T) {
	t.Parallel()

	perPeer := 8
	cache := newSeenMessagesCache(time.Minute, perPeer*4, perPeer)

	for i := 0; i < perPeer*4; i++ {
		assert.False(t, cache.hasOrAdd("peer", uint64(i)))
		assert.LessOrEqual(t, cache.len(), perPeer)
	}

	assert.Equal(t, perPeer, cache.len())
	assert.False(t, cache.has("peer", 0))
	assert.True(t, cache.has("peer", uint64(perPeer*4-1)))
}

func TestSeenMessagesCacheStaysBoundedUnderSustainedFreshSeqnos(t *testing.T) {
	t.Parallel()

	maxEntries := 16
	cache := newSeenMessagesCache(time.Hour, maxEntries, 4)

	for i := 0; i < 10000; i++ {
		cache.hasOrAdd(fmt.Sprintf("peer-%d", i%(maxEntries*2)), uint64(i))
	}

	assert.Equal(t, maxEntries, cache.len())
}

// TestSeenMessagesCacheFloodingPeersEvictOnlyThemselves is the oracle for the cross-peer eviction
// findings. The victim holds a *full* bucket, so any policy that charges the largest holder takes
// from it; the flood is spread across identities that each stay smaller than the victim, so any
// policy that charges a fair share takes from it too. Only a hard per-peer quota leaves it alone.
func TestSeenMessagesCacheFloodingPeersEvictOnlyThemselves(t *testing.T) {
	t.Parallel()

	perPeer := 16
	maxEntries := perPeer * 32
	cache := newSeenMessagesCache(time.Hour, maxEntries, perPeer)

	for i := 0; i < perPeer; i++ {
		assert.False(t, cache.hasOrAdd("victim", uint64(i)))
	}

	sybils := 30
	for i := 0; i < maxEntries*4; i++ {
		cache.hasOrAdd(fmt.Sprintf("sybil-%d", i%sybils), uint64(i))
	}

	for i := 0; i < perPeer; i++ {
		assert.True(t, cache.has("victim", uint64(i)), "victim seqno %d was evicted by another peer", i)
	}
	assert.LessOrEqual(t, cache.len(), maxEntries)
}

// A source rotating identities presses on the peer bound, not the entry bound. Once every slot
// holds a live bucket the newcomers go untracked; they do not take a live peer's slot.
func TestSeenMessagesCacheIdentityRotationDoesNotEvictLivePeer(t *testing.T) {
	t.Parallel()

	maxPeers := 8
	cache := newSeenMessagesCache(time.Hour, maxPeers*4, 4)

	assert.False(t, cache.hasOrAdd("victim", 1))

	for i := 0; i < maxPeers*10; i++ {
		cache.hasOrAdd(fmt.Sprintf("sybil-%d", i), 1)
	}

	assert.True(t, cache.has("victim", 1))

	cache.mut.Lock()
	defer cache.mut.Unlock()

	assert.Equal(t, maxPeers, len(cache.peers))
	assert.Equal(t, maxPeers, cache.byTouch.Len())
}

// The fail-open, pinned so a change to it is deliberate: a newcomer that finds every slot live is
// not deduplicated until a slot frees.
func TestSeenMessagesCacheUntrackedNewcomerIsNotDeduplicated(t *testing.T) {
	t.Parallel()

	maxPeers := 4
	cache := newSeenMessagesCache(time.Hour, maxPeers*4, 4)

	for i := 0; i < maxPeers; i++ {
		cache.hasOrAdd(fmt.Sprintf("peer-%d", i), 1)
	}

	assert.False(t, cache.hasOrAdd("newcomer", 1))
	assert.False(t, cache.hasOrAdd("newcomer", 1))
	assert.False(t, cache.has("newcomer", 1))
}

// Expired entries in a bucket nobody touches again must not keep the peer bound pinned: the idle
// bucket is reclaimed by the next newcomer, which is then tracked normally.
func TestSeenMessagesCacheReclaimsIdleBucketForNewcomer(t *testing.T) {
	t.Parallel()

	maxPeers := 4
	cache := newSeenMessagesCache(time.Millisecond*10, maxPeers*4, 4)

	for i := 0; i < maxPeers; i++ {
		cache.hasOrAdd(fmt.Sprintf("peer-%d", i), 1)
	}
	time.Sleep(time.Millisecond * 30)

	assert.False(t, cache.hasOrAdd("newcomer", 1))
	assert.True(t, cache.hasOrAdd("newcomer", 1))

	cache.mut.Lock()
	defer cache.mut.Unlock()

	assert.Equal(t, maxPeers, len(cache.peers))
}

func TestSeenMessagesCacheClampsNonPositiveBounds(t *testing.T) {
	t.Parallel()

	for _, cache := range []*seenMessagesCache{
		newSeenMessagesCache(time.Minute, 0, 0),
		newSeenMessagesCache(time.Minute, -1, -1),
	} {
		// a non-positive bound must not reinstate the unbounded growth this type exists to prevent
		assert.Equal(t, seenMessagesPerPeer, cache.perPeer)
		assert.Equal(t, defaultMaxSeenMessages/seenMessagesPerPeer, cache.maxPeers)
	}

	// a total smaller than one share still tracks one peer rather than none
	assert.Equal(t, 1, newSeenMessagesCache(time.Minute, 1, 64).maxPeers)
}

func TestSeenMessagesCacheConcurrentHasOrAdd(t *testing.T) {
	t.Parallel()

	perPeer := 32
	maxPeers := 6
	cache := newSeenMessagesCache(time.Millisecond*5, perPeer*maxPeers, perPeer)

	goroutines := 16
	perGoroutine := 500

	wg := sync.WaitGroup{}
	wg.Add(goroutines)
	for g := 0; g < goroutines; g++ {
		go func(g int) {
			defer wg.Done()

			for i := 0; i < perGoroutine; i++ {
				// more peers than slots and more seqnos than quota, so every path runs
				cache.hasOrAdd(fmt.Sprintf("peer-%d", (g+i)%(maxPeers*2)), uint64(i))
			}
		}(g)
	}
	wg.Wait()

	cache.mut.Lock()
	defer cache.mut.Unlock()

	assert.LessOrEqual(t, len(cache.peers), maxPeers)
	require.Equal(t, len(cache.peers), cache.byTouch.Len(), "peer map and LRU must not drift")

	for el := cache.byTouch.Front(); el != nil; el = el.Next() {
		bucket := el.Value.(*peerSeen)
		require.Same(t, el, cache.peers[bucket.id], "every LRU element must be the map's element")
		require.LessOrEqual(t, len(bucket.seen), perPeer)
		require.Equal(t, perPeer, cap(bucket.seen), "bucket must keep its fixed capacity")

		for i := 1; i < len(bucket.seen); i++ {
			require.False(t, bucket.seen[i].at.Before(bucket.seen[i-1].at), "seen must stay in time order")
		}
	}
}

// BenchmarkSeenMessagesCacheFullBucketMiss is the per-frame worst case at the production
// constants: the peer's bucket is at quota with live entries, every seqno is fresh, so each call
// walks the whole bucket and then drops the oldest.
func BenchmarkSeenMessagesCacheFullBucketMiss(b *testing.B) {
	cache := newSeenMessagesCache(timeSeenMessages, defaultMaxSeenMessages, seenMessagesPerPeer)

	for i := 0; i < seenMessagesPerPeer; i++ {
		cache.hasOrAdd("peer", uint64(i))
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cache.hasOrAdd("peer", uint64(seenMessagesPerPeer+i))
	}
}

// BenchmarkSeenMessagesCacheUntrackedNewcomer is the refused path: every slot holds a live bucket
// and a peer without one keeps sending. It must stay O(1), or a pinned table would turn each of
// those frames into a scan under the node-wide lock.
func BenchmarkSeenMessagesCacheUntrackedNewcomer(b *testing.B) {
	cache := newSeenMessagesCache(timeSeenMessages, defaultMaxSeenMessages, seenMessagesPerPeer)

	for i := 0; i < cache.maxPeers; i++ {
		cache.hasOrAdd(fmt.Sprintf("peer-%d", i), 1)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		cache.hasOrAdd("newcomer", uint64(i))
	}
}

// refusalWarningCounter counts emissions of the replay-cache refusal warning for one cache,
// picked out by the span value only that test uses. The log subject writes to every observer
// synchronously — an empty slice when the formatter filters a line out — and this package runs
// tests in parallel, so the writer has to be safe under concurrent writes and blind to other
// caches' warnings.
type refusalWarningCounter struct {
	mut  sync.Mutex
	n    int
	span string
}

func (c *refusalWarningCounter) Write(p []byte) (int, error) {
	if len(p) > 0 {
		c.mut.Lock()
		c.n++
		c.mut.Unlock()
	}

	return len(p), nil
}

func (c *refusalWarningCounter) count() int {
	c.mut.Lock()
	defer c.mut.Unlock()

	return c.n
}

// Output is the Formatter: a byte for a matching line, nothing for anything else.
func (c *refusalWarningCounter) Output(line logger.LogLineHandler) []byte {
	if line.GetMessage() != "direct-send replay cache full: new peer not deduplicated" {
		return nil
	}
	for _, arg := range line.GetArgs() {
		if arg == c.span {
			return []byte{1}
		}
	}

	return nil
}

func (c *refusalWarningCounter) IsInterfaceNil() bool { return c == nil }

// TestSeenMessagesCacheRefusalWarningIsBoundedPerSpan pins the property that makes the fail-open
// warning safe to emit at all. Filling the table is peer-triggerable, so a line per refusal would
// be a log lever; the warning must fire once, hold for the span however many newcomers are
// refused, and fire again once the span has passed. It counts the lines the logger actually
// emits: an earlier version watched the rate-limit timestamp instead, and passed with the log
// call hoisted outside the guard — a thousand warnings — and with the log call deleted.
func TestSeenMessagesCacheRefusalWarningIsBoundedPerSpan(t *testing.T) {
	const span = 37 * time.Millisecond // unique to this test, so parallel emitters do not count

	counter := &refusalWarningCounter{span: span.String()}
	require.NoError(t, logger.AddLogObserver(counter, counter))
	defer func() { _ = logger.RemoveLogObserver(counter) }()

	smc := newSeenMessagesCache(span, 2*seenMessagesPerPeer, seenMessagesPerPeer)
	smc.hasOrAdd("held-a", 1)
	smc.hasOrAdd("held-b", 1)
	require.Equal(t, 0, counter.count(), "no refusal yet, so nothing to warn about")

	for i := 0; i < 1000; i++ {
		require.False(t, smc.hasOrAdd(fmt.Sprintf("newcomer-%d", i), 1))
	}
	require.Equal(t, 1, counter.count(),
		"1000 refusals inside one span must produce exactly one warning")

	// held-a and held-b go idle past the span, so the next newcomer is admitted, not refused —
	// re-fill the table first so the refusal path is what fires again.
	time.Sleep(span + 5*time.Millisecond)
	smc.hasOrAdd("held-c", 1)
	smc.hasOrAdd("held-d", 1)
	require.False(t, smc.hasOrAdd("newcomer-late", 1))
	require.Equal(t, 2, counter.count(),
		"once the span has passed the warning must re-arm, or a sustained condition is reported once ever")
}
