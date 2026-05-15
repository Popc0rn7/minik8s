MINIK8S ?= ./minik8s
CNI_PLUGIN ?= .minik8s/cni/bin/minik8s-bridge
HARBOR ?= http://127.0.0.1:18080
CTL ?= env MINIK8S_HARBOR=$(HARBOR) $(MINIK8S)
RUN ?= $(MINIK8S)

.PHONY: build test bridge sailer-once sailer cni-init doctor-network apply-nginx apply-client apply-volume get-pods get-demo-pods clean-nginx clean-client clean-volume clean-cases

build:
	go build -o $(MINIK8S) ./cmd/minik8s
	go build -o $(CNI_PLUGIN) ./cmd/minik8s-bridge

test:
	go test ./...

bridge: build
	$(MINIK8S) bridge --listen :18080

sailer-once: build
	$(RUN) sailer --node-name $(or $(NODE_NAME),node-a) --harbor $(HARBOR) $(if $(NODE_IP),--node-ip $(NODE_IP),) $(if $(POD_CIDR),--pod-cidr $(POD_CIDR),) --once

sailer: build
	$(RUN) sailer --node-name $(or $(NODE_NAME),node-a) --harbor $(HARBOR) $(if $(NODE_IP),--node-ip $(NODE_IP),) $(if $(POD_CIDR),--pod-cidr $(POD_CIDR),)

cni-init: build
	$(RUN) cni init

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
