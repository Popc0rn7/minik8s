package logbook

import (
	"errors"
	"path/filepath"
	"testing"

	"minik8s/internal/dns"
)

func TestInMemoryDNSStoreCRUD(t *testing.T) {
	store := NewInMemoryDNSStore()
	obj := dns.New("web", "default", dns.DNSSpec{Host: "example.com", Paths: []dns.DNSPath{{
		Path: "/api", PathType: dns.PathTypePrefix, ServiceName: "api", ServicePort: 80,
	}}})

	if err := store.Create(obj); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := store.Create(obj); !errors.Is(err, ErrDNSAlreadyExists) {
		t.Fatalf("Create duplicate error = %v, want ErrDNSAlreadyExists", err)
	}
	got, err := store.Get("web", "default")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	got.Spec.Host = "changed.example.com"
	again, _ := store.Get("web", "default")
	if again.Spec.Host != "example.com" {
		t.Fatalf("store returned mutable DNS object")
	}
	obj.Spec.Host = "new.example.com"
	if err := store.Update(obj); err != nil {
		t.Fatalf("Update() error = %v", err)
	}
	list, err := store.List("default", nil)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(list) != 1 || list[0].Spec.Host != "new.example.com" {
		t.Fatalf("unexpected list: %#v", list)
	}
	if err := store.Delete("web", "default"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Get("web", "default"); !errors.Is(err, ErrDNSNotFound) {
		t.Fatalf("Get deleted error = %v, want ErrDNSNotFound", err)
	}
}

func TestFileDNSStorePersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "dns.json")
	store, err := NewFileDNSStore(path)
	if err != nil {
		t.Fatalf("NewFileDNSStore() error = %v", err)
	}
	obj := dns.New("web", "", dns.DNSSpec{Host: "example.com", Paths: []dns.DNSPath{{
		Path: "/", PathType: dns.PathTypePrefix, ServiceName: "web", ServicePort: 80,
	}}})
	if err := store.Create(obj); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	reloaded, err := NewFileDNSStore(path)
	if err != nil {
		t.Fatalf("reload NewFileDNSStore() error = %v", err)
	}
	got, err := reloaded.Get("web", "default")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if got.Spec.Host != "example.com" {
		t.Fatalf("host = %q", got.Spec.Host)
	}
}
