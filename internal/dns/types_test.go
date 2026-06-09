package dns

import (
	"testing"

	"minik8s/internal/pod"
)

func TestDNSDeepCopyCopiesPathsAndLabels(t *testing.T) {
	d := &DNS{
		ObjectMeta: pod.ObjectMeta{Name: "routes", Namespace: "default", Labels: map[string]string{"app": "web"}},
		Spec: DNSSpec{
			Host: "example.com",
			Paths: []DNSPath{{
				Path:        "/api",
				PathType:    PathTypePrefix,
				ServiceName: "api",
				ServicePort: 80,
			}},
		},
	}

	copy := d.DeepCopy()
	copy.Labels["app"] = "changed"
	copy.Spec.Paths[0].Path = "/changed"

	if d.Labels["app"] != "web" {
		t.Fatalf("labels were not deep-copied")
	}
	if d.Spec.Paths[0].Path != "/api" {
		t.Fatalf("paths were not deep-copied")
	}
}
