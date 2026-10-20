package slowrequest

import (
	"sort"
	"sync"
)

const (
	// MaxEndpoints bounds the distinct endpoint/method pairs held between
	// drains. Endpoint grouping keeps the real set far below it.
	MaxEndpoints = 32
	// OtherEndpoint collects whatever arrives once MaxEndpoints is reached.
	OtherEndpoint = "other"
)

// Count is the slow requests seen for one endpoint and method since the last
// drain. It is the wire shape of the heartbeat's slow_requests entries.
type Count struct {
	Endpoint string `json:"endpoint"`
	Method   string `json:"method"`
	Count    int64  `json:"count"`
	MaxMs    int64  `json:"max_ms"`
}

type countKey struct {
	endpoint string
	method   string
}

// Counts accumulates slow requests between heartbeats. It is safe for
// concurrent use.
type Counts struct {
	mu      sync.Mutex
	entries map[countKey]*Count
}

// Default is the process-wide counts every Reporter from New writes to and
// the heartbeat drains.
var Default = NewCounts()

// NewCounts returns empty counts.
func NewCounts() *Counts {
	return &Counts{entries: map[countKey]*Count{}}
}

func (c *Counts) record(endpoint, method string, ms int64) {
	c.add(Count{Endpoint: endpoint, Method: method, Count: 1, MaxMs: ms})
}

func (c *Counts) add(n Count) {
	c.mu.Lock()
	defer c.mu.Unlock()

	key := countKey{endpoint: n.Endpoint, method: n.Method}
	if _, ok := c.entries[key]; !ok && len(c.entries) >= MaxEndpoints {
		key = countKey{endpoint: OtherEndpoint}
	}

	entry, ok := c.entries[key]
	if !ok {
		entry = &Count{Endpoint: key.endpoint, Method: key.method}
		c.entries[key] = entry
	}
	entry.Count += n.Count
	if n.MaxMs > entry.MaxMs {
		entry.MaxMs = n.MaxMs
	}
}

// Drain returns the counts accumulated since the last drain, ordered by
// endpoint and method with the overflow entry last, and empties them.
func (c *Counts) Drain() []Count {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) == 0 {
		return nil
	}
	drained := make([]Count, 0, len(c.entries))
	for _, entry := range c.entries {
		drained = append(drained, *entry)
	}
	c.entries = map[countKey]*Count{}

	sort.Slice(drained, func(i, j int) bool {
		a, b := drained[i], drained[j]
		if (a.Endpoint == OtherEndpoint) != (b.Endpoint == OtherEndpoint) {
			return b.Endpoint == OtherEndpoint
		}
		if a.Endpoint != b.Endpoint {
			return a.Endpoint < b.Endpoint
		}
		return a.Method < b.Method
	})
	return drained
}

// Restore merges back counts a drain took but could not deliver, so a failed
// heartbeat does not lose them. Delivery is at least once: a heartbeat the
// control plane recorded but whose response was lost is counted again.
func (c *Counts) Restore(counts []Count) {
	for _, n := range counts {
		c.add(n)
	}
}
