package harbor

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDNSCRUD(t *testing.T) {
	server := New(Config{})
	body := []byte(`{"kind":"DNS","apiVersion":"v1","metadata":{"name":"web"},"spec":{"host":"example.com","paths":[{"path":"/","serviceName":"web","servicePort":80}]}}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/namespaces/default/dns", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", rec.Code, rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/api/v1/namespaces/default/dns", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rec.Code, rec.Body.String())
	}
	var list struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("list length = %d", len(list.Items))
	}

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/namespaces/default/dns/web", nil)
	rec = httptest.NewRecorder()
	server.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete status = %d body=%s", rec.Code, rec.Body.String())
	}
}

func TestAPIResourcesIncludesDNS(t *testing.T) {
	rec := httptest.NewRecorder()
	New(Config{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), `"name":"dns"`) {
		t.Fatalf("api resources missing dns: %s", rec.Body.String())
	}
}
