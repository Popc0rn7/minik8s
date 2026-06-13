package captain

import (
	"context"
	"sync"
	"time"

	"minik8s/internal/minilog"
)

const (
	ServiceControllerName    = "service-periodic-sync"
	ReplicaSetControllerName = "replicaset-periodic-sync"
	HPAControllerName        = "hpa-periodic-sync"
	NodeLivenessName         = "node-liveness-sync"
)

// Controller reconciles one control-plane concern.
type Controller interface {
	Name() string
	Sync(ctx context.Context) error
}

type ControllerFunc struct {
	ControllerName string
	SyncFunc       func(context.Context) error
}

func (c ControllerFunc) Name() string { return c.ControllerName }

func (c ControllerFunc) Sync(ctx context.Context) error {
	if c.SyncFunc == nil {
		return nil
	}
	return c.SyncFunc(ctx)
}

// RunSpec configures how a controller is driven by Runner.
type RunSpec struct {
	Interval    time.Duration
	InitialSync bool
	// Deprecated: controller syncs are now coalesced through a global serial
	// queue, so this field is retained only for compatibility.
	SkipIfRunning bool
}

// Runner drives registered controllers and serializes store-heavy work.
type Runner struct {
	mu          sync.RWMutex
	controllers map[string]registeredController
	startOnce   sync.Once
	workerOnce  sync.Once

	queueMu sync.Mutex
	queue   []string
	states  map[string]*controllerRunState
	notify  chan struct{}
}

type registeredController struct {
	controller Controller
	spec       RunSpec
}

type controllerRunState struct {
	running     bool
	pendingDone chan struct{}
	pendingCtx  context.Context
	rerunDone   chan struct{}
	rerunCtx    context.Context
}

// NewRunner creates an empty controller runner.
func NewRunner() *Runner {
	return &Runner{
		controllers: make(map[string]registeredController),
		states:      make(map[string]*controllerRunState),
		notify:      make(chan struct{}, 1),
	}
}

// Register stores or replaces one controller by name.
func (r *Runner) Register(controller Controller, spec RunSpec) {
	if controller == nil || controller.Name() == "" {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.controllers[controller.Name()] = registeredController{controller: controller, spec: spec}
}

// Start launches periodic loops for all registered controllers.
func (r *Runner) Start(ctx context.Context) {
	r.startOnce.Do(func() {
		r.startWorker()
		for _, registered := range r.snapshot() {
			go r.runLoop(ctx, registered)
		}
	})
}

// RunOnce executes a named controller once. It returns false when the controller
// is unknown or the context is canceled before the queued sync completes.
func (r *Runner) RunOnce(ctx context.Context, name string) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if _, ok := r.get(name); !ok {
		return false
	}
	r.startWorker()
	done := r.enqueue(ctx, name)
	select {
	case <-ctx.Done():
		return false
	case <-done:
		return true
	}
}

func (r *Runner) runLoop(ctx context.Context, registered registeredController) {
	if registered.spec.InitialSync {
		r.enqueue(ctx, registered.controller.Name())
	}
	if registered.spec.Interval <= 0 {
		return
	}
	ticker := time.NewTicker(registered.spec.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.enqueue(ctx, registered.controller.Name())
		}
	}
}

func (r *Runner) startWorker() {
	r.workerOnce.Do(func() {
		go r.worker()
	})
}

func (r *Runner) enqueue(ctx context.Context, name string) <-chan struct{} {
	if ctx == nil {
		ctx = context.Background()
	}
	r.queueMu.Lock()
	defer r.queueMu.Unlock()

	state := r.state(name)
	if state.running {
		if state.rerunDone == nil {
			state.rerunDone = make(chan struct{})
			state.rerunCtx = ctx
		}
		return state.rerunDone
	}
	if state.pendingDone != nil {
		return state.pendingDone
	}

	state.pendingDone = make(chan struct{})
	state.pendingCtx = ctx
	r.queue = append(r.queue, name)
	r.signalWorker()
	return state.pendingDone
}

func (r *Runner) worker() {
	for {
		name, ctx, done := r.next()
		registered, ok := r.get(name)
		if ok {
			r.run(ctx, registered)
		}
		r.finish(name, done)
	}
}

func (r *Runner) next() (string, context.Context, chan struct{}) {
	for {
		r.queueMu.Lock()
		if len(r.queue) > 0 {
			name := r.queue[0]
			r.queue = r.queue[1:]
			state := r.state(name)
			done := state.pendingDone
			ctx := state.pendingCtx
			state.pendingDone = nil
			state.pendingCtx = nil
			state.running = true
			r.queueMu.Unlock()
			return name, ctx, done
		}
		r.queueMu.Unlock()
		<-r.notify
	}
}

func (r *Runner) finish(name string, done chan struct{}) {
	r.queueMu.Lock()
	defer r.queueMu.Unlock()

	if done != nil {
		close(done)
	}
	state := r.state(name)
	state.running = false
	if state.rerunDone != nil {
		state.pendingDone = state.rerunDone
		state.pendingCtx = state.rerunCtx
		state.rerunDone = nil
		state.rerunCtx = nil
		r.queue = append(r.queue, name)
		r.signalWorker()
		return
	}
	if state.pendingDone == nil {
		delete(r.states, name)
	}
}

func (r *Runner) run(ctx context.Context, registered registeredController) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := registered.controller.Sync(ctx); err != nil {
		minilog.Warn(registered.controller.Name(), "error=%v", err)
	}
}

func (r *Runner) state(name string) *controllerRunState {
	state := r.states[name]
	if state == nil {
		state = &controllerRunState{}
		r.states[name] = state
	}
	return state
}

func (r *Runner) signalWorker() {
	select {
	case r.notify <- struct{}{}:
	default:
	}
}

func (r *Runner) get(name string) (registeredController, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	registered, ok := r.controllers[name]
	return registered, ok
}

func (r *Runner) snapshot() []registeredController {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]registeredController, 0, len(r.controllers))
	for _, registered := range r.controllers {
		result = append(result, registered)
	}
	return result
}
