/*
This is free and unencumbered software released into the public domain. For more
information, see <http://unlicense.org/> or the accompanying UNLICENSE file.
*/

package circuit

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

func successfulAction() func() error {
	return func() error {
		return nil
	}
}

var errFail = errors.New("fail")

func failingAction() func() error {
	return func() error {
		return errFail
	}
}

func blockingAction(wg *sync.WaitGroup, quit chan struct{}) func() error {
	return func() error {
		// Signal the action was started
		wg.Done()
		<-quit
		return nil
	}
}

func TestBreakerClosed(t *testing.T) {
	b := &Breaker{}

	done := false
	action := func() error {
		done = true
		return nil
	}

	err := b.Do(context.Background(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !done {
		t.Errorf("action not executed, wanted it to be executed")
	}
}

func TestBreakerOpen(t *testing.T) {
	b := &Breaker{}

	done := false
	action := func() error {
		done = true
		return nil
	}

	b.open(OpenReasonThreshold)
	err := b.Do(context.Background(), action)

	if err != ErrCircuitOpen {
		t.Fatalf("got error %v, wanted ErrCircuitOpen", err)
	}

	if done {
		t.Errorf("action executed, wanted it not to be executed")
	}
}

func TestBreakerHalfOpenFirstRequest(t *testing.T) {
	b := &Breaker{}

	done := false
	action := func() error {
		done = true
		return nil
	}

	b.reset()
	err := b.Do(context.Background(), action)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !done {
		t.Errorf("action not executed, wanted it to be executed")
	}
}

func TestBreakerHalfOpenSecondRequest(t *testing.T) {
	b := &Breaker{}

	done := false
	action := func() error {
		done = true
		return nil
	}

	var wg sync.WaitGroup
	wg.Add(1)

	quit := make(chan struct{})
	defer close(quit)

	b.reset()
	go b.Do(context.Background(), blockingAction(&wg, quit))

	// Wait for the first action to start and block
	wg.Wait()

	// Issue another action. It should not be allowed to execute.
	err := b.Do(context.Background(), action)

	if err != ErrCircuitOpen {
		t.Fatalf("got error %v, wanted ErrCircuitOpen", err)
	}

	if done {
		t.Errorf("action executed, wanted it not to be executed")
	}
}

func TestBreakerOpenWithThreshold(t *testing.T) {
	b := &Breaker{
		Threshold: 5,
	}

	if !b.IsClosed() {
		t.Fatalf("breaker was not in closed state")
	}

	// Send 5 failing requests

	for i := 0; i < 4; i++ {
		if err := b.Do(context.Background(), failingAction()); err != errFail {
			t.Fatalf("unexpected error: %v", err)
		}
		if !b.IsClosed() {
			t.Fatalf("request %d: breaker was not in closed state", i)
		}
	}

	if err := b.Do(context.Background(), failingAction()); err != errFail {
		t.Fatalf("unexpected error: %v", err)
	}

	if !b.IsOpen() {
		t.Fatalf("breaker was not in open state")
	}
}

func TestBreakerFailuresMustBeConsecutive(t *testing.T) {
	b := &Breaker{
		Threshold: 5,
	}

	if !b.IsClosed() {
		t.Fatalf("breaker was not in closed state")
	}

	// Send 3 failing requests, 1 succeeding and 1 failing.

	for i := 0; i < 3; i++ {
		if err := b.Do(context.Background(), failingAction()); err != errFail {
			t.Fatalf("unexpected error: %v", err)
		}
		if !b.IsClosed() {
			t.Fatalf("request %d: breaker was not in closed state", i)
		}
	}

	if err := b.Do(context.Background(), successfulAction()); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if err := b.Do(context.Background(), failingAction()); err != errFail {
		t.Fatalf("unexpected error: %v", err)
	}

	if !b.IsClosed() {
		t.Fatalf("breaker was not in closed state")
	}
}

func TestBreakerOpenWithConcurrent(t *testing.T) {
	b := &Breaker{
		Concurrency: 3,
	}

	if !b.IsClosed() {
		t.Fatalf("breaker was not in closed state")
	}

	// Send 3 blocking requests and then one more normal one

	var wg sync.WaitGroup
	wg.Add(3)

	quit := make(chan struct{})
	defer close(quit)

	for i := 0; i < 3; i++ {
		go func() {
			b.Do(context.Background(), blockingAction(&wg, quit))
		}()
	}
	wg.Wait()

	if err := b.Do(context.Background(), successfulAction()); err != ErrTooManyConcurrent {
		t.Fatalf("unexpected error: %v", err)
	}

	if !b.IsOpen() {
		t.Fatalf("breaker was not in open state")
	}
}

func TestBreakerResetTimeout(t *testing.T) {
	b := &Breaker{
		Threshold:    2,
		ResetTimeout: 20 * time.Millisecond,
	}

	if !b.IsClosed() {
		t.Fatalf("breaker was not in closed state")
	}

	b.Do(context.Background(), failingAction())
	b.Do(context.Background(), failingAction())

	if !b.IsOpen() {
		t.Fatalf("breaker was not in open state")
	}

	time.Sleep(20 * time.Millisecond)
	if !b.IsHalfOpen() {
		time.Sleep(20 * time.Millisecond)
	}

	if !b.IsHalfOpen() {
		t.Fatalf("breaker was not in half-open state")
	}
}

func TestBreakerContextCanceled(t *testing.T) {
	b := &Breaker{}

	ctx, cancelFn := context.WithCancel(context.Background())
	cancelFn()

	// Should return the context error
	if err := b.Do(ctx, successfulAction()); err != ctx.Err() {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestBreakerOnResetCalledBeforeHalfOpenState(t *testing.T) {
	b := &Breaker{
		Threshold:    2,
		ResetTimeout: 20 * time.Millisecond,
	}

	onResetCalled := false

	b.OnReset = func() {
		onResetCalled = true
	}

	if !b.IsClosed() {
		t.Fatalf("breaker was not in closed state")
	}

	b.Do(context.Background(), failingAction())
	b.Do(context.Background(), failingAction())

	if !b.IsOpen() {
		t.Fatalf("breaker was not in open state")
	}

	time.Sleep(20 * time.Millisecond)
	if !b.IsHalfOpen() {
		time.Sleep(20 * time.Millisecond)
	}

	if !b.IsHalfOpen() {
		t.Fatalf("breaker was not in half-open state")
	}

	b.Do(context.Background(), func() error {
		if !onResetCalled {
			t.Errorf("expected OnReset to have been called")
		}
		return nil
	})
}
