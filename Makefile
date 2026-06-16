MINIK8S ?= ./minik8s
KUBECTL ?= ./kubectl
CNI_PLUGIN ?= .minik8s/cni/bin/mooring
HARBOR ?= http://127.0.0.1:18080
CTL ?= $(KUBECTL)
RUN ?= $(MINIK8S)

PROD_IMAGE ?= golang:1.25.9-bookworm
PROD_DIR ?= dist/prod
PROD_GOOS ?= linux
PROD_GOARCH ?= amd64
PROD_GOPROXY ?= https://goproxy.cn,https://proxy.golang.org,direct
PROD_DOCKER_USER ?= $(shell id -u):$(shell id -g)
MOORING_CNI_IMAGE ?= ghcr.io/popc0rn7/mooring-cni
GPU_SUBMITTER_IMAGE ?= ghcr.io/popc0rn7/gpu-submitter
IMAGE_TAG ?= latest
PLATFORM ?= linux/amd64
DEPLOY_ARGS ?=
DEMO_DIR ?= demo/serverless/harbor-incident-triage

.PHONY: build prod prod-build prod-push prod-cni prod-demo mooring-cni-image push-mooring-cni-image gpu-submitter-image push-gpu-submitter-image deploy-prod prod-deploy test bridge sailer-once sailer cni-init doctor-network apply-nginx apply-client apply-volume get-pods get-demo-pods clean-nginx clean-client clean-volume clean-cases

build:
	go build -o $(MINIK8S) ./cmd/minik8s
	go build -o $(KUBECTL) ./cmd/kubectl
	go build -o $(CNI_PLUGIN) ./cmd/mooring

prod: prod-build prod-push

prod-build:
	PROD_IMAGE="$(PROD_IMAGE)" PROD_DIR="$(PROD_DIR)" PROD_GOOS="$(PROD_GOOS)" PROD_GOARCH="$(PROD_GOARCH)" PROD_GOPROXY="$(PROD_GOPROXY)" PROD_DOCKER_USER="$(PROD_DOCKER_USER)" scripts/prod-build.sh

prod-push:
	scripts/deploy-prod.sh --sync-only

prod-cni: mooring-cni-image push-mooring-cni-image
	MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/deploy-prod.sh --pull-image-only

prod-demo:
	DEMO_DIR="$(DEMO_DIR)" scripts/deploy-demo.sh

mooring-cni-image:
	MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" PLATFORM="$(PLATFORM)" scripts/build-mooring-cni-image.sh

push-mooring-cni-image:
	MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/push-mooring-cni-image.sh

gpu-submitter-image:
	GPU_SUBMITTER_IMAGE="$(GPU_SUBMITTER_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" PLATFORM="$(PLATFORM)" scripts/build-gpu-submitter-image.sh

push-gpu-submitter-image:
	GPU_SUBMITTER_IMAGE="$(GPU_SUBMITTER_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/push-gpu-submitter-image.sh

deploy-prod:
	MOORING_CNI_IMAGE="$(MOORING_CNI_IMAGE)" IMAGE_TAG="$(IMAGE_TAG)" scripts/deploy-prod.sh $(DEPLOY_ARGS)

prod-deploy: prod-build prod-push prod-cni

test:
	go test ./...

bridge: build
	$(MINIK8S) bridge --listen :18080

sailer-once: build
	$(RUN) sailer $(or $(NODE_FILE),manifest/node/node_a.yaml) --harbor $(HARBOR) --once

sailer: build
	$(RUN) sailer $(or $(NODE_FILE),manifest/node/node_a.yaml) --harbor $(HARBOR)

cni-init: build
	$(RUN) cni init

doctor:
	$(RUN) doctor network

apply-nginx:
	$(CTL) apply -f manifest/pod/pod_nginx.yaml

apply-client:
	$(CTL) apply -f manifest/pod/pod_busybox_client.yaml

apply-volume:
	mkdir -p /tmp/minik8s-case-data
	$(CTL) apply -f manifest/pod/pod_volume_resource.yaml

ps:
	$(CTL) get pods

clean-nginx:
	-$(CTL) delete pod nginx-pod

clean-client:
	-$(CTL) delete pod busybox-client

clean-volume:
	-$(CTL) delete pod volume-resource-pod -n demo

clean: clean-client clean-nginx clean-volume
