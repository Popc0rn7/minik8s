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

func TestRunnerRunOnceSkipsConcurrentWork(t *testing.T) {
	runner := NewRunner()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan struct{})
	var secondRuns atomic.Int32
	runner.Register(testController{name: "first", sync: func(context.Context) error {
		close(started)
		<-release
		return nil
	}}, RunSpec{SkipIfRunning: true})
	runner.Register(testController{name: "second", sync: func(context.Context) error {
		secondRuns.Add(1)
		return nil
	}}, RunSpec{SkipIfRunning: true})

	go func() {
		assert.True(t, runner.RunOnce(context.Background(), "first"))
		close(done)
	}()
	<-started

	assert.False(t, runner.RunOnce(context.Background(), "second"))
	assert.Equal(t, int32(0), secondRuns.Load())
	close(release)
	<-done
	assert.True(t, runner.RunOnce(context.Background(), "second"))
	assert.Equal(t, int32(1), secondRuns.Load())
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
