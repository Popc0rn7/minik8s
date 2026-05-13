MINIK8S ?= ./minik8s
CNI_PLUGIN ?= .minik8s/cni/bin/minik8s-bridge
APISERVER ?= http://127.0.0.1:18080
CTL ?= env MINIK8S_APISERVER=$(APISERVER) $(MINIK8S)
RUN ?= sudo $(MINIK8S)

.PHONY: build test apiserver kubelet-once kubelet cni-init net-registry netd-once doctor-network apply-nginx apply-client apply-volume get-pods get-demo-pods clean-nginx clean-client clean-volume clean-cases

build:
	go build -o $(MINIK8S) ./cmd/minik8s
	go build -o $(CNI_PLUGIN) ./cmd/minik8s-bridge

test:
	go test ./...

apiserver: build
	$(MINIK8S) apiserver --listen :18080

kubelet-once: build
	$(RUN) kubelet --node-name $(or $(NODE_NAME),node-a) --apiserver $(APISERVER) --once

kubelet: build
	$(RUN) kubelet --node-name $(or $(NODE_NAME),node-a) --apiserver $(APISERVER)

cni-init: build
	$(RUN) cni init

net-registry: build
	$(MINIK8S) net-registry --listen :8088

netd-once: build
	$(RUN) netd --once --node-name $(NODE_NAME) --node-ip $(NODE_IP) --pod-cidr $(POD_CIDR) --registry $(REGISTRY)

doctor:
	$(RUN) doctor network

apply-nginx:
	$(CTL) apply -f manifest/testdata/pod_nginx.yaml

apply-client:
	$(CTL) apply -f manifest/testdata/pod_busybox_client.yaml

apply-volume:
	mkdir -p /tmp/minik8s-case-data
	$(CTL) apply -f manifest/testdata/pod_volume_resource.yaml

ps:
	$(CTL) get pods

clean-nginx:
	-$(CTL) delete pod nginx-pod

clean-client:
	-$(CTL) delete pod busybox-client

clean-volume:
	-$(CTL) delete pod volume-resource-pod -n demo

clean: clean-client clean-nginx clean-volume
