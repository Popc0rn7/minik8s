package kubeproxy

import (
	"context"

	"minik8s/internal/service"
)

// Proxy reconciles Minik8s Service state into node-local forwarding rules.
type Proxy interface {
	SyncService(ctx context.Context, svc *service.Service) error
	SyncAll(ctx context.Context, services []*service.Service) error
	DeleteService(ctx context.Context, svc *service.Service) error
}
