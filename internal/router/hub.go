package router

import (
	"sync"
	"sync/atomic"
)

// subscriberBuffer is the number of buffers a subscriber may fall behind
// before the hub starts dropping audio for it.
const subscriberBuffer = 64

// hub copies every buffer from one source to all of its subscribers.
// Buffers are shared, never modified. A slow subscriber loses buffers rather
// than delaying the others.
type hub struct {
	mu     sync.Mutex
	subs   map[*subscription]struct{}
	closed bool
	done   chan struct{}
}

type subscription struct {
	ch      chan []float32
	counter *atomic.Int64 // samples delivered, shared per route
	dropped atomic.Int64
}

func newHub(in <-chan []float32) *hub {
	h := &hub{subs: make(map[*subscription]struct{}), done: make(chan struct{})}
	go h.run(in)
	return h
}

func (h *hub) run(in <-chan []float32) {
	defer close(h.done)
	for buf := range in {
		h.mu.Lock()
		for s := range h.subs {
			select {
			case s.ch <- buf:
				s.counter.Add(int64(len(buf)))
			default:
				s.dropped.Add(int64(len(buf)))
			}
		}
		h.mu.Unlock()
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.closed = true
	for s := range h.subs {
		close(s.ch)
		delete(h.subs, s)
	}
}

// subscribe returns a new subscription. If the source has ended, its
// channel is already closed.
func (h *hub) subscribe(counter *atomic.Int64) *subscription {
	s := &subscription{ch: make(chan []float32, subscriberBuffer), counter: counter}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		close(s.ch)
	} else {
		h.subs[s] = struct{}{}
	}
	return s
}

func (h *hub) unsubscribe(s *subscription) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
}
