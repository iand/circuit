/*
This is free and unencumbered software released into the public domain. For more
information, see <http://unlicense.org/> or the accompanying UNLICENSE file.
*/

// Package circuit provides an implementation of the circuit-breaker pattern which can be used to
// improve stability in distributed systems.
package circuit

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// Breaker is a circuit breaker. The circuit breaker can be in one of
// three states: closed (requests will be executed normally), open (requests will be
// rejected immediately) or half-open (a single request will be used to determine whether
// to move to the open or closed states)
// During normal operation the breaker is in the closed state. When a request fails a
// counter is incremented. A successful request will reset the counter. When the failure
// counter reaches a threshold, indicating a consecutive series of failures, the breaker
// will trip and move to the open state.
// In the open state all requests will fail immediately, returning the ErrCircuitOpen error.
// A timer is started and after the reset timeout, the breaker will move into the half-open state.
// In the half-open state the first call is used to trial the system. During this trial all
// other requests will fail as though the breaker were in the open state. If the trialing
// request succeeds the breaker is moved to the closed (normal) state. Otherwise the
// breaker moves back to the open state and the reset timer is restarted.
// In the closed and half-open states, a count of the number of concurrent requests is maintained. This
// number rises above the configured maximum then the breaker will trip into the open state.
type Breaker struct {
	// Threshold controls the number of consecutive errors that are allowed before the
	// circuit breaker trips open. If zero a default of 20 will be assumed.
	Threshold uint32

	// Concurrency controls the number of concurrent requests that are allowed before
	// the circuit breaker trips open.  If zero a default of 10 will be assumed.
	Concurrency uint32

	// Reset timeout is the time to wait once the circuit breaker trips open until it
	// should be put into the half-open state.  If zero a default of 10 seconds will be assumed.
	ResetTimeout time.Duration

	// OnOpen is a function that will be called when the circuit breaker trips open. If it
	// is nil then it will be ignored.
	OnOpen func(OpenReason)

	// OnClose is a function that will be called when the circuit breaker closes. If it
	// is nil then it will be ignored.
	OnClose func()

	// mu ensures only one state transition can occur at a time
	mu     sync.Mutex
	initer sync.Once

	// Current state of the circuit breaker: closed, open, half-open
	state uint32

	// A count of consecutive failures
	failures uint32

	// handles limit the number of concurrent requests
	handles chan struct{}

	// Keeps track of whether the circuit has trialed a request in the half-open state
	attemptedTrial uint32
}

const (
	closed   uint32 = 0
	open     uint32 = 1
	halfopen uint32 = 2
)

const (
	defaultThreshold    uint32        = 20
	defaultConcurrency  uint32        = 10
	defaultResetTimeout time.Duration = 10 * time.Second
)

var (
	// ErrCircuitOpen is returned when a request is made while the circuit is open
	ErrCircuitOpen = errors.New("circuit is open")

	// ErrTooManyConcurrent is returned when a request would exceed the concurrency level of the breaker.
	ErrTooManyConcurrent = errors.New("too many concurrent requests")
)

// An OpenReason indicates why the circuit breaker opened.
type OpenReason int

const (
	// OpenReasonThreshold means the circuit opened because the failure threshold was reached
	OpenReasonThreshold OpenReason = 0 //

	// OpenReasonConcurrency means the circuit opened because the concurrency limit was reached
	OpenReasonConcurrency OpenReason = 1

	// OpenReasonTrial means the circuit opened because the trial request failed
	OpenReasonTrial OpenReason = 2
)

// Do attempts to execute the supplied function. If the function is executed
// any error it produces is treated as a failure, incrementing the breaker's
// counter. The error, if any, is returned from Do. If there are too many
// concurrent requests then fn will not be executed and ErrTooManyConcurrent
// will be returned. When the breaker is in the open state then fn will not be
// executed and ErrCircuitOpen error will be returned.
func (b *Breaker) Do(ctx context.Context, fn func() error) error {
	b.initer.Do(b.init)

	if ctx != nil {
		// Check whether context has been cancelled
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}

	state := atomic.LoadUint32(&b.state)

	switch state {
	case closed:
		// Try the action
		err := b.attempt(fn)
		switch err {
		case nil:
			// reset the consecutive failure count
			atomic.StoreUint32(&b.failures, 0)
			return nil
		case ErrTooManyConcurrent:
			b.open(OpenReasonConcurrency)
			return err
		default:
			// record a failure
			failures := atomic.AddUint32(&b.failures, 1)
			if failures >= b.Threshold {
				b.open(OpenReasonThreshold)
			}
			return err
		}

	case open:
		// Fast fail in the open state
		return ErrCircuitOpen

	case halfopen:
		// Check if this is the first request since circuit was half opened
		if atomic.CompareAndSwapUint32(&b.attemptedTrial, 0, 1) {
			err := b.attempt(fn)
			if err != nil {
				b.open(OpenReasonTrial)
				return err
			}

			b.close()
			return nil
		}

		return ErrCircuitOpen
	default:
		panic("unreachable")
	}
}

func (b *Breaker) attempt(fn func() error) error {
	select {
	case <-b.handles:
	default:
		return ErrTooManyConcurrent
	}

	defer func() {
		b.handles <- struct{}{}
	}()

	return fn()
}

func (b *Breaker) open(r OpenReason) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if atomic.LoadUint32(&b.state) == open {
		return
	}
	atomic.StoreUint32(&b.state, open)
	atomic.StoreUint32(&b.failures, 0)
	atomic.StoreUint32(&b.attemptedTrial, 0)
	time.AfterFunc(b.ResetTimeout, b.reset)

	if b.OnOpen != nil {
		b.OnOpen(r)
	}
}

func (b *Breaker) close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if atomic.LoadUint32(&b.state) == closed {
		return
	}
	atomic.StoreUint32(&b.state, closed)
	atomic.StoreUint32(&b.failures, 0)
	atomic.StoreUint32(&b.attemptedTrial, 0)
	if b.OnClose != nil {
		b.OnClose()
	}
}

// reset puts the breaker into half-open mode, usually after the reset timeout has passed
func (b *Breaker) reset() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if atomic.LoadUint32(&b.state) == halfopen {
		return
	}
	atomic.StoreUint32(&b.state, halfopen)
	atomic.StoreUint32(&b.failures, 0)
	atomic.StoreUint32(&b.attemptedTrial, 0)
}

func (b *Breaker) init() {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.Concurrency == 0 {
		b.Concurrency = defaultConcurrency
	}

	if b.Threshold == 0 {
		b.Threshold = defaultThreshold
	}

	if b.ResetTimeout == 0 {
		b.ResetTimeout = defaultResetTimeout
	}

	b.handles = make(chan struct{}, int(b.Concurrency))
	for i := uint32(0); i < b.Concurrency; i++ {
		b.handles <- struct{}{}
	}
}

// IsClosed reports whether the circuit breaker is in the closed state
func (b *Breaker) IsClosed() bool {
	return atomic.LoadUint32(&b.state) == closed
}

// IsOpen reports whether the circuit breaker is in the open state
func (b *Breaker) IsOpen() bool {
	return atomic.LoadUint32(&b.state) == open
}

// IsHalfOpen reports whether the circuit breaker is in the half-open state
func (b *Breaker) IsHalfOpen() bool {
	return atomic.LoadUint32(&b.state) == halfopen
}
