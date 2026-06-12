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
	Interval      time.Duration
	InitialSync   bool
	SkipIfRunning bool
}

// Runner drives registered controllers and serializes store-heavy work.
type Runner struct {
	mu          sync.RWMutex
	controllers map[string]registeredController
	syncMu      sync.Mutex
	startOnce   sync.Once
}

type registeredController struct {
	controller Controller
	spec       RunSpec
}

// NewRunner creates an empty controller runner.
func NewRunner() *Runner {
	return &Runner{controllers: make(map[string]registeredController)}
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
		for _, registered := range r.snapshot() {
			go r.runLoop(ctx, registered)
		}
	})
}

// RunOnce executes a named controller once. It returns false when the controller
// is unknown or skipped because another controller is already syncing.
func (r *Runner) RunOnce(ctx context.Context, name string) bool {
	registered, ok := r.get(name)
	if !ok {
		return false
	}
	return r.run(ctx, registered)
}

func (r *Runner) runLoop(ctx context.Context, registered registeredController) {
	if registered.spec.InitialSync {
		r.run(ctx, registered)
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
			r.run(ctx, registered)
		}
	}
}

func (r *Runner) run(ctx context.Context, registered registeredController) bool {
	if registered.spec.SkipIfRunning {
		if !r.syncMu.TryLock() {
			minilog.Warn("bridge-sync-skip", "sync=%s reason=busy", registered.controller.Name())
			return false
		}
		defer r.syncMu.Unlock()
	} else {
		r.syncMu.Lock()
		defer r.syncMu.Unlock()
	}
	if err := registered.controller.Sync(ctx); err != nil {
		minilog.Warn(registered.controller.Name(), "error=%v", err)
	}
	return true
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
