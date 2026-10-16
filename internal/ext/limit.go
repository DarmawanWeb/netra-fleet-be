package ext

import (
	"math"
	"strconv"
	"sync"
	"time"
)

// buckets is a token-bucket rate limiter per client.
type buckets struct {
	mu    sync.Mutex
	rate  float64 // tokens per second
	burst float64
	state map[int64]*bucket
	now   func() time.Time
}

type bucket struct {
	tokens float64
	at     time.Time
}

func newBuckets(rate, burst float64) *buckets {
	return &buckets{rate: rate, burst: burst, state: map[int64]*bucket{}, now: time.Now}
}

// take spends one token; it returns 0 when allowed, else how long until a token is available.
func (b *buckets) take(id int64) time.Duration {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.now()
	s, ok := b.state[id]
	if !ok {
		s = &bucket{tokens: b.burst, at: now}
		b.state[id] = s
	}
	s.tokens = math.Min(b.burst, s.tokens+now.Sub(s.at).Seconds()*b.rate)
	s.at = now
	if s.tokens >= 1 {
		s.tokens--
		return 0
	}
	return time.Duration((1 - s.tokens) / b.rate * float64(time.Second))
}

// retryAfter formats a wait as whole seconds for the Retry-After header (at least 1).
func retryAfter(d time.Duration) string {
	return strconv.Itoa(int(math.Max(1, math.Ceil(d.Seconds()))))
}

// failures blocks an address after repeated bad client credentials.
type failures struct {
	mu    sync.Mutex
	times map[string][]time.Time
}

const (
	maxFailures   = 10
	failureWindow = 10 * time.Minute
)

func newFailures() *failures { return &failures{times: map[string][]time.Time{}} }

func (f *failures) blocked(ip string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	recent := f.times[ip][:0]
	for _, t := range f.times[ip] {
		if time.Since(t) < failureWindow {
			recent = append(recent, t)
		}
	}
	if len(recent) == 0 {
		delete(f.times, ip)
		return false
	}
	f.times[ip] = recent
	return len(recent) >= maxFailures
}

func (f *failures) add(ip string) {
	f.mu.Lock()
	f.times[ip] = append(f.times[ip], time.Now())
	f.mu.Unlock()
}

func (f *failures) reset(ip string) {
	f.mu.Lock()
	delete(f.times, ip)
	f.mu.Unlock()
}
