package captain

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type testController struct {
	name string
	sync func(context.Context) error
}

func (c testController) Name() string { return c.name }

func (c testController) Sync(ctx context.Context) error {
	if c.sync == nil {
		return nil
	}
	return c.sync(ctx)
}

func TestRunnerRunOnceSerializesConcurrentWork(t *testing.T) {
	runner := NewRunner()
	started := make(chan struct{})
	release := make(chan struct{})
	firstDone := make(chan struct{})
	secondDone := make(chan struct{})
	var secondRuns atomic.Int32
	var active atomic.Int32
	var maxActive atomic.Int32
	var secondAttempted atomic.Int32
	runner.Register(testController{name: "first", sync: func(context.Context) error {
		current := active.Add(1)
		maxActive.Store(current)
		close(started)
		<-release
		active.Add(-1)
		return nil
	}}, RunSpec{SkipIfRunning: true})
	runner.Register(testController{name: "second", sync: func(context.Context) error {
		current := active.Add(1)
		maxActive.Store(current)
		secondRuns.Add(1)
		active.Add(-1)
		return nil
	}}, RunSpec{SkipIfRunning: true})

	go func() {
		assert.True(t, runner.RunOnce(context.Background(), "first"))
		close(firstDone)
	}()
	<-started

	go func() {
		secondAttempted.Add(1)
		assert.True(t, runner.RunOnce(context.Background(), "second"))
		close(secondDone)
	}()
	require.Eventually(t, func() bool {
		return secondAttempted.Load() == 1
	}, time.Second, time.Millisecond)
	assert.Equal(t, int32(0), secondRuns.Load())
	close(release)
	<-firstDone
	<-secondDone
	assert.Equal(t, int32(1), secondRuns.Load())
	assert.Equal(t, int32(1), maxActive.Load())
}

func TestRunnerCoalescesSameControllerWhileRunning(t *testing.T) {
	runner := NewRunner()
	started := make(chan struct{})
	release := make(chan struct{})
	var runs atomic.Int32
	runner.Register(testController{name: "coalesced", sync: func(context.Context) error {
		run := runs.Add(1)
		if run == 1 {
			close(started)
			<-release
		}
		return nil
	}}, RunSpec{SkipIfRunning: true})

	firstDone := make(chan bool)
	go func() {
		firstDone <- runner.RunOnce(context.Background(), "coalesced")
	}()
	<-started

	done := make(chan bool, 5)
	var attempted atomic.Int32
	for i := 0; i < cap(done); i++ {
		go func() {
			attempted.Add(1)
			done <- runner.RunOnce(context.Background(), "coalesced")
		}()
	}
	require.Eventually(t, func() bool {
		return attempted.Load() == int32(cap(done))
	}, time.Second, time.Millisecond)

	assert.Equal(t, int32(1), runs.Load())
	close(release)
	assert.True(t, <-firstDone)
	for i := 0; i < cap(done); i++ {
		assert.True(t, <-done)
	}
	assert.Equal(t, int32(2), runs.Load())
}

func TestRunnerStartRunsInitialAndPeriodicSync(t *testing.T) {
	runner := NewRunner()
	var count atomic.Int32
	runner.Register(testController{name: "periodic", sync: func(context.Context) error {
		count.Add(1)
		return nil
	}}, RunSpec{Interval: time.Millisecond, InitialSync: true, SkipIfRunning: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner.Start(ctx)

	require.Eventually(t, func() bool {
		return count.Load() >= 2
	}, time.Second, time.Millisecond)
}

func TestRunnerContinuesAfterControllerError(t *testing.T) {
	runner := NewRunner()
	var count atomic.Int32
	runner.Register(testController{name: "flaky", sync: func(context.Context) error {
		if count.Add(1) == 1 {
			return errors.New("boom")
		}
		return nil
	}}, RunSpec{Interval: time.Millisecond, InitialSync: true, SkipIfRunning: true})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	runner.Start(ctx)

	require.Eventually(t, func() bool {
		return count.Load() >= 2
	}, time.Second, time.Millisecond)
}

func TestRunnerRunOnceUnknownController(t *testing.T) {
	runner := NewRunner()

	assert.False(t, runner.RunOnce(context.Background(), "missing"))
}
