MINIK8S ?= ./minik8s
CNI_PLUGIN ?= .minik8s/cni/bin/minik8s-bridge
KUBEHARBOR ?= http://127.0.0.1:18080
CTL ?= env MINIK8S_KUBEHARBOR=$(KUBEHARBOR) $(MINIK8S)
RUN ?= sudo $(MINIK8S)

.PHONY: build test kubebridge kubesailer-once kubesailer cni-init net-registry netd-once doctor-network apply-nginx apply-client apply-volume get-pods get-demo-pods clean-nginx clean-client clean-volume clean-cases

build:
	go build -o $(MINIK8S) ./cmd/minik8s
	go build -o $(CNI_PLUGIN) ./cmd/minik8s-bridge

test:
	go test ./...

kubebridge: build
	$(MINIK8S) kubebridge --listen :18080

kubesailer-once: build
	$(RUN) kubesailer --node-name $(or $(NODE_NAME),node-a) --kubeharbor $(KUBEHARBOR) --once

kubesailer: build
	$(RUN) kubesailer --node-name $(or $(NODE_NAME),node-a) --kubeharbor $(KUBEHARBOR)

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
