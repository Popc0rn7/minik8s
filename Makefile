MINIK8S ?= ./minik8s
KUBECTL ?= ./kubectl
CNI_PLUGIN ?= .minik8s/cni/bin/mooring
HARBOR ?= http://127.0.0.1:18080
CTL ?= $(KUBECTL)
RUN ?= $(MINIK8S)
GO_BUILD ?= CGO_ENABLED=0 go build -trimpath -tags "netgo osusergo"

PROD_DIR ?= .
REMOTE_DIR ?= /opt/minik8s
SSH ?=
DEPLOY_NODES ?=
SSH_OPTS ?=
MOORING_CNI_IMAGE ?= ghcr.io/popc0rn7/mooring-cni
GPU_SUBMITTER_IMAGE ?= ghcr.io/popc0rn7/gpu-submitter
IMAGE_TAG ?= v0.1.0
PLATFORM ?= linux/amd64
DEPLOY_ARGS ?=
DEMO_DIR ?= demo/serverless/harbor-incident-triage

.PHONY: build prod prod-build prod-push prod-cni prod-demo prod-verify mooring-cni-image push-mooring-cni-image gpu-submitter-image push-gpu-submitter-image deploy-prod prod-deploy test bridge sailer-join sailer cni-init doctor-network apply-pod-acceptance apply-service-acceptance apply-rs-acceptance get-pods clean-pod-acceptance clean-service-acceptance clean-rs-acceptance clean-cases

build:
	$(GO_BUILD) -o $(MINIK8S) ./cmd/minik8s
	$(GO_BUILD) -o $(KUBECTL) ./cmd/kubectl
	$(GO_BUILD) -o $(CNI_PLUGIN) ./cmd/mooring

prod: prod-build

prod-build: build

prod-push:
	PROD_DIR="$(PROD_DIR)" DEPLOY_NODES="$(DEPLOY_NODES)" SSH_OPTS="$(SSH_OPTS)" REMOTE_DIR="$(REMOTE_DIR)" scripts/deploy-prod.sh --sync-only

prod-cni: mooring-cni-image push-mooring-cni-image

prod-demo:
	DEMO_DIR="$(DEMO_DIR)" scripts/deploy-demo.sh

prod-verify:
	@test -n "$(SSH)" || { printf 'usage: make prod-verify SSH="ssh root@10.119.16.213 -i ~/.ssh/id_ed25519_minik8s"\n' >&2; exit 2; }
	$(SSH) 'cd "$(REMOTE_DIR)" && ./bin/minik8s --help >/dev/null && ./bin/kubectl --help >/dev/null && test -d manifests && test -d scripts/acceptance'

mooring-cni-image:
	MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" PLATFORM="$(PLATFORM)" scripts/build-mooring-cni-image.sh

push-mooring-cni-image:
	MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/push-mooring-cni-image.sh

gpu-submitter-image:
	GPU_SUBMITTER_IMAGE="$(GPU_SUBMITTER_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" PLATFORM="$(PLATFORM)" scripts/build-gpu-submitter-image.sh

push-gpu-submitter-image:
	GPU_SUBMITTER_IMAGE="$(GPU_SUBMITTER_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/push-gpu-submitter-image.sh

deploy-prod:
	PROD_DIR="$(PROD_DIR)" DEPLOY_NODES="$(DEPLOY_NODES)" SSH_OPTS="$(SSH_OPTS)" REMOTE_DIR="$(REMOTE_DIR)" MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/deploy-prod.sh $(DEPLOY_ARGS)

prod-deploy: deploy-prod

test:
	go test ./...

bridge: build
	$(MINIK8S) bridge --listen :18080

NODE_NAME ?= node-a
MINIK8S_TOKEN ?= minik8s

sailer-join: build
	$(RUN) sailer join --apiserver $(HARBOR) --token $(MINIK8S_TOKEN) --node-name $(NODE_NAME)

sailer: build
	$(RUN) sailer run

cni-init: build
	$(RUN) cni init

doctor:
	$(RUN) doctor network

apply-pod-acceptance:
	$(CTL) apply -f manifests/pod/pod_02_acceptance_main.yaml

apply-service-acceptance:
	$(CTL) apply -f manifests/service/pod_03_nginx_node_a.yaml
	$(CTL) apply -f manifests/service/pod_03_nginx_node_b.yaml
	$(CTL) apply -f manifests/service/service_03_clusterip.yaml

apply-rs-acceptance:
	$(CTL) apply -f manifests/replicaset/replicaset_04_acceptance.yaml

ps:
	$(CTL) get pods

clean-pod-acceptance:
	-$(CTL) delete pod pod-02-main

clean-service-acceptance:
	-$(CTL) delete service svc-03-clusterip
	-$(CTL) delete pod svc-03-nginx-a
	-$(CTL) delete pod svc-03-nginx-b

clean-rs-acceptance:
	-$(CTL) delete service rs-04-web
	-$(CTL) delete rs rs-04-web

clean: clean-pod-acceptance clean-service-acceptance clean-rs-acceptance
