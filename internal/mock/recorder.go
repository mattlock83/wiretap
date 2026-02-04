package mock

import "sync"

// Recorder stores exchanges in arrival order.
type Recorder struct {
	mu        sync.Mutex
	exchanges []Exchange
}

func NewRecorder() *Recorder {
	return &Recorder{exchanges: make([]Exchange, 0)}
}

func (r *Recorder) Record(exchange Exchange) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exchanges = append(r.exchanges, exchange)
}

func (r *Recorder) Exchanges() []Exchange {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Exchange, len(r.exchanges))
	copy(out, r.exchanges)
	return out
}

func (r *Recorder) Clear() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exchanges = r.exchanges[:0]
}
