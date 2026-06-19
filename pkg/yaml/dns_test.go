package yaml

import "testing"

func TestLoadDNSFromYAMLDefaultsAndValidates(t *testing.T) {
	data := []byte(`
kind: DNS
apiVersion: v1
metadata:
  name: web-routes
spec:
  host: example.com
  paths:
    - path: /api
      serviceName: api
      servicePort: 80
`)

	d, err := LoadDNSFromYAML(data)
	if err != nil {
		t.Fatalf("LoadDNSFromYAML() error = %v", err)
	}
	if d.Kind != "DNS" || d.Namespace != "default" {
		t.Fatalf("unexpected defaults: kind=%q namespace=%q", d.Kind, d.Namespace)
	}
	if d.Spec.Paths[0].PathType != "Prefix" {
		t.Fatalf("default pathType = %q, want Prefix", d.Spec.Paths[0].PathType)
	}
}

func TestLoadDNSFromYAMLRejectsInvalidPath(t *testing.T) {
	data := []byte(`
kind: DNS
metadata:
  name: bad
spec:
  host: example.com
  paths:
    - path: api
      serviceName: api
      servicePort: 80
`)

	if _, err := LoadDNSFromYAML(data); err == nil {
		t.Fatalf("LoadDNSFromYAML() succeeded, want invalid path error")
	}
}
