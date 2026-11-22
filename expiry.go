package keepcache

import "time"

// entry is a cached item that also lives in the expiry heap at position index.
type entry[K comparable, V any] struct {
	key       K
	value     V
	ttl       time.Duration
	expiresAt time.Time
	index     int
}

// expiryHeap is a min-heap of entries ordered by expiresAt. It implements
// container/heap.Interface; entries track their own index so they can be
// fixed or removed in O(log n).
type expiryHeap[K comparable, V any] []*entry[K, V]

func (h expiryHeap[K, V]) Len() int { return len(h) }

func (h expiryHeap[K, V]) Less(i, j int) bool { return h[i].expiresAt.Before(h[j].expiresAt) }

func (h expiryHeap[K, V]) Swap(i, j int) {
	h[i], h[j] = h[j], h[i]
	h[i].index = i
	h[j].index = j
}

func (h *expiryHeap[K, V]) Push(x any) {
	e := x.(*entry[K, V])
	e.index = len(*h)
	*h = append(*h, e)
}

func (h *expiryHeap[K, V]) Pop() any {
	old := *h
	n := len(old)
	e := old[n-1]
	old[n-1] = nil
	*h = old[:n-1]
	return e
}

func (e *entry[K, V]) expired(now time.Time) bool { return !now.Before(e.expiresAt) }
