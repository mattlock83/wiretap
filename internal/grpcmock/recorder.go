package grpcmock

import "sync"

// Recorder stores gRPC exchanges for later retrieval.
type Recorder struct {
	mu        sync.Mutex
	exchanges []Exchange
}

// NewRecorder creates a recorder instance.
func NewRecorder() *Recorder {
	return &Recorder{}
}

// Record adds an exchange to the recorder.
func (r *Recorder) Record(exchange Exchange) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exchanges = append(r.exchanges, exchange)
}

// Exchanges returns a snapshot of recorded exchanges.
func (r *Recorder) Exchanges() []Exchange {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Exchange, len(r.exchanges))
	copy(out, r.exchanges)
	return out
}

// Clear removes all recorded exchanges.
func (r *Recorder) Clear() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.exchanges = nil
}
