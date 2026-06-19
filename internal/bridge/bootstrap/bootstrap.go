package bootstrap

import (
	"context"
	"fmt"
	"sync"
	"time"

	"minik8s/internal/metrics"
	"minik8s/internal/node"
	"minik8s/internal/pod"
	"minik8s/internal/sailer"
	"minik8s/internal/service"
)

const (
	DefaultNodeName = "minik8s-bridge-local"

	AnnotationInternal = "minik8s.internal"
)

type PrivatePodClient struct {
	mu       sync.Mutex
	node     *node.Node
	pods     map[string]*pod.Pod
	statuses map[string]pod.PodStatus
}

func NewPrivatePodClient(n *node.Node, pods ...*pod.Pod) *PrivatePodClient {
	items := make(map[string]*pod.Pod, len(pods))
	for _, p := range pods {
		if p == nil {
			continue
		}
		items[podKey(p.Namespace, p.Name)] = p.DeepCopy()
	}
	return &PrivatePodClient{
		node:     n.DeepCopy(),
		pods:     items,
		statuses: make(map[string]pod.PodStatus, len(items)),
	}
}

func (c *PrivatePodClient) ListAssignedPods(ctx context.Context, heartbeat sailer.NodeHeartbeat) ([]*pod.Pod, error) {
	_ = ctx
	if heartbeat.Node == nil {
		return nil, fmt.Errorf("node heartbeat is required")
	}
	if heartbeat.Node.Name() != c.node.Name() {
		return nil, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make([]*pod.Pod, 0, len(c.pods))
	for _, p := range c.pods {
		copy := p.DeepCopy()
		if status, ok := c.statuses[podKey(copy.Namespace, copy.Name)]; ok {
			copy.Status = status.DeepCopy()
		}
		items = append(items, copy)
	}
	return items, nil
}

func (c *PrivatePodClient) ListServices(ctx context.Context) ([]*service.Service, error) {
	_ = ctx
	return nil, nil
}

func (c *PrivatePodClient) GetClusterConfig(ctx context.Context) (*sailer.ClusterConfig, error) {
	_ = ctx
	return &sailer.ClusterConfig{ClusterDomain: "cluster.local"}, nil
}

func (c *PrivatePodClient) UpdatePodStatus(ctx context.Context, p *pod.Pod) error {
	_ = ctx
	if p == nil {
		return fmt.Errorf("pod is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	key := podKey(p.Namespace, p.Name)
	if _, ok := c.pods[key]; !ok {
		return fmt.Errorf("unknown private pod %s", key)
	}
	c.statuses[key] = p.Status.DeepCopy()
	return nil
}

func (c *PrivatePodClient) UpdateNodeStatus(ctx context.Context, nodeName string, status node.NodeStatus) error {
	_, _, _ = ctx, nodeName, status
	return nil
}

func (c *PrivatePodClient) UpdateNodeMetrics(ctx context.Context, nodeName string, podMetrics []*metrics.PodMetrics) error {
	_, _, _ = ctx, nodeName, podMetrics
	return nil
}

func (c *PrivatePodClient) SetPods(pods ...*pod.Pod) {
	c.mu.Lock()
	defer c.mu.Unlock()
	items := make(map[string]*pod.Pod, len(pods))
	statuses := make(map[string]pod.PodStatus, len(pods))
	for _, p := range pods {
		if p == nil {
			continue
		}
		key := podKey(p.Namespace, p.Name)
		items[key] = p.DeepCopy()
		if status, ok := c.statuses[key]; ok {
			statuses[key] = status
		}
	}
	c.pods = items
	c.statuses = statuses
}

func (c *PrivatePodClient) PodStatus(namespace, name string) (pod.PodStatus, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	status, ok := c.statuses[podKey(namespace, name)]
	if !ok {
		return pod.PodStatus{}, false
	}
	return status.DeepCopy(), true
}

func DefaultNode() *node.Node {
	return node.New(DefaultNodeName, node.NodeSpec{
		Role:    node.NodeRoleWorker,
		PodCIDR: "127.0.0.1/32",
	}, node.NodeStatus{
		Phase: node.NodeReady,
		Addresses: []node.NodeAddress{{
			Type:    node.NodeAddressInternalIP,
			Address: "127.0.0.1",
		}},
		LastHeartbeat: time.Now().UTC(),
	})
}

func DependencyPod(etcdDataDir string) *pod.Pod {
	return StoragePod(etcdDataDir)
}

func StoragePod(etcdDataDir string) *pod.Pod {
	return &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      "storage-etcd",
			Namespace: "minik8s-system",
			Labels: map[string]string{
				"app":          "storage-etcd",
				"minik8s.kind": "bridge-storage",
			},
			Annotations: map[string]string{
				AnnotationInternal: "true",
			},
		},
		Spec: pod.PodSpec{
			NodeName:      DefaultNodeName,
			RestartPolicy: pod.RestartPolicyAlways,
			Volumes: []pod.VolumeSpec{{
				Name:     "etcd-data",
				HostPath: &pod.HostPathVolume{Path: etcdDataDir},
			}},
			Containers: []pod.ContainerSpec{
				{
					Name:     "etcd",
					Image:    "quay.io/coreos/etcd",
					ImageTag: "v3.5.15",
					Command:  []string{"/usr/local/bin/etcd"},
					Args: []string{
						"--name", "minik8s-etcd",
						"--data-dir", "/etcd-data",
						"--listen-client-urls", "http://0.0.0.0:2379",
						"--advertise-client-urls", "http://127.0.0.1:2379",
						"--listen-peer-urls", "http://127.0.0.1:2380",
						"--initial-advertise-peer-urls", "http://127.0.0.1:2380",
						"--initial-cluster", "minik8s-etcd=http://127.0.0.1:2380",
						"--initial-cluster-state", "new",
					},
					Ports: []pod.ContainerPort{{
						ContainerPort: 2379,
						HostPort:      2379,
						Protocol:      "TCP",
					}},
					VolumeMounts: []pod.VolumeMount{{
						Name:      "etcd-data",
						MountPath: "/etcd-data",
					}},
				},
			},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
}

type DNSPodOptions struct {
	ConfigDir            string
	DNSHostIP            string
	DNSHostPort          int32
	IngressHostPort      int32
	RouteProxyBinaryPath string
}

func DNSPod(configDir string, dnsHostPort, ingressHostPort int32) *pod.Pod {
	return DNSPodWithOptions(DNSPodOptions{
		ConfigDir:            configDir,
		DNSHostPort:          dnsHostPort,
		IngressHostPort:      ingressHostPort,
		RouteProxyBinaryPath: "/opt/minik8s/bin/minik8s",
	})
}

func DNSPodWithOptions(options DNSPodOptions) *pod.Pod {
	if options.RouteProxyBinaryPath == "" {
		options.RouteProxyBinaryPath = "/opt/minik8s/bin/minik8s"
	}
	return &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      "dns-gateway",
			Namespace: "minik8s-system",
			Labels: map[string]string{
				"app":          "dns-gateway",
				"minik8s.kind": "bridge-dns",
			},
			Annotations: map[string]string{
				AnnotationInternal: "true",
			},
		},
		Spec: pod.PodSpec{
			NodeName:      DefaultNodeName,
			RestartPolicy: pod.RestartPolicyAlways,
			Volumes: []pod.VolumeSpec{{
				Name:     "dns-config",
				HostPath: &pod.HostPathVolume{Path: options.ConfigDir},
			}, {
				Name:     "route-proxy-bin",
				HostPath: &pod.HostPathVolume{Path: options.RouteProxyBinaryPath},
			}},
			Containers: []pod.ContainerSpec{
				{
					Name:     "coredns",
					Image:    "coredns/coredns",
					ImageTag: "1.11.1",
					Args:     []string{"-conf", "/minik8s-dns/Corefile"},
					Ports: []pod.ContainerPort{{
						ContainerPort: 53,
						HostIP:        options.DNSHostIP,
						HostPort:      options.DNSHostPort,
						Protocol:      "UDP",
					}, {
						ContainerPort: 53,
						HostIP:        options.DNSHostIP,
						HostPort:      options.DNSHostPort,
						Protocol:      "TCP",
					}},
					VolumeMounts: []pod.VolumeMount{{
						Name:      "dns-config",
						MountPath: "/minik8s-dns",
					}},
				},
				{
					Name:     "nginx",
					Image:    "nginx",
					ImageTag: "1.27-alpine",
					Command:  []string{"nginx"},
					Args:     []string{"-c", "/minik8s-dns/nginx.conf", "-g", "daemon off;"},
					Ports: []pod.ContainerPort{{
						ContainerPort: 80,
						HostPort:      options.IngressHostPort,
						Protocol:      "TCP",
					}},
					VolumeMounts: []pod.VolumeMount{{
						Name:      "dns-config",
						MountPath: "/minik8s-dns",
					}},
				},
				{
					Name:     "route-proxy",
					Image:    "alpine",
					ImageTag: "3.20",
					Command:  []string{"/usr/local/bin/minik8s"},
					Args:     []string{"route-proxy", "--listen", "127.0.0.1:18081", "--routes", "/minik8s-dns/routes.json"},
					VolumeMounts: []pod.VolumeMount{{
						Name:      "dns-config",
						MountPath: "/minik8s-dns",
					}, {
						Name:      "route-proxy-bin",
						MountPath: "/usr/local/bin/minik8s",
						ReadOnly:  true,
					}},
				},
			},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
}

func ServerlessNATSPod() *pod.Pod {
	return &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      "serverless-nats",
			Namespace: "minik8s-system",
			Labels: map[string]string{
				"app":          "serverless-nats",
				"minik8s.kind": "serverless-event-bus",
			},
			Annotations: map[string]string{
				AnnotationInternal: "true",
			},
		},
		Spec: pod.PodSpec{
			NodeName:      DefaultNodeName,
			RestartPolicy: pod.RestartPolicyAlways,
			Containers: []pod.ContainerSpec{{
				Name:     "nats",
				Image:    "nats",
				ImageTag: "2",
				Ports: []pod.ContainerPort{{
					ContainerPort: 4222,
					HostPort:      4222,
					Protocol:      "TCP",
				}},
			}},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
}

func MetricsServerPod() *pod.Pod {
	return &pod.Pod{
		TypeMeta: pod.TypeMeta{Kind: "Pod", APIVersion: "v1"},
		ObjectMeta: pod.ObjectMeta{
			Name:      "metrics-server",
			Namespace: "minik8s-system",
			Labels: map[string]string{
				"app":          "metrics-server",
				"minik8s.kind": "metrics-adapter",
			},
			Annotations: map[string]string{
				AnnotationInternal: "true",
			},
		},
		Spec: pod.PodSpec{
			NodeName:      DefaultNodeName,
			RestartPolicy: pod.RestartPolicyAlways,
			Containers: []pod.ContainerSpec{{
				Name:     "metrics-server",
				Image:    "busybox",
				ImageTag: "1.36",
				Command:  []string{"sh", "-c"},
				Args:     []string{"sleep 365d"},
			}},
		},
		Status: pod.PodStatus{Phase: pod.PodPending},
	}
}

func podKey(namespace, name string) string {
	if namespace == "" {
		namespace = "default"
	}
	return namespace + "/" + name
}
