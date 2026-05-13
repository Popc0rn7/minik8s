MINIK8S ?= ./minik8s
CNI_PLUGIN ?= .minik8s/cni/bin/minik8s-bridge
RUN ?= sudo $(MINIK8S)

.PHONY: build test cni-init net-registry netd-once doctor-network apply-nginx apply-client apply-volume get-pods get-demo-pods clean-nginx clean-client clean-volume clean-cases

build:
	go build -o $(MINIK8S) ./cmd/minik8s
	go build -o $(CNI_PLUGIN) ./cmd/minik8s-bridge

test:
	go test ./...

cni-init: build
	$(RUN) cni init

net-registry: build
	$(MINIK8S) net-registry --listen :8088

netd-once: build
	$(RUN) netd --once --node-name $(NODE_NAME) --node-ip $(NODE_IP) --pod-cidr $(POD_CIDR) --registry $(REGISTRY)

doctor:
	$(RUN) doctor network

apply-nginx:
	$(RUN) apply -f manifest/testdata/pod_nginx.yaml

apply-client:
	$(RUN) apply -f manifest/testdata/pod_busybox_client.yaml

apply-volume:
	mkdir -p /tmp/minik8s-case-data
	$(RUN) apply -f manifest/testdata/pod_volume_resource.yaml

ps:
	$(RUN) get pods

clean-nginx:
	-$(RUN) delete pod nginx-pod

clean-client:
	-$(RUN) delete pod busybox-client

clean-volume:
	-$(RUN) delete pod volume-resource-pod -n demo

clean: clean-client clean-nginx clean-volume
