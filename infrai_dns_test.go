package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEnsureDomainAddsWhenLookupHasNoZoneID(t *testing.T) {
	var added bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case domainGetPath:
			if r.Method != http.MethodGet || r.URL.Query().Get("domain") != "creator.example" {
				t.Fatalf("unexpected domain lookup: %s %s", r.Method, r.URL.String())
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]string{"zone_id": ""}})
		case domainAddPath:
			added = true
			var body map[string]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if r.Method != http.MethodPost || body["domain"] != "creator.example" || len(body) != 1 {
				t.Fatalf("unexpected domain add: %s %#v", r.Method, body)
			}
			json.NewEncoder(w).Encode(map[string]any{"ok": true, "data": map[string]string{"zone_id": "zone-123"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewDNSClient(server.URL, "test-key")
	zoneID, err := client.EnsureDomain(context.Background(), "creator.example")
	if err != nil {
		t.Fatal(err)
	}
	if zoneID != "zone-123" || !added {
		t.Fatalf("EnsureDomain() = %q, added = %v", zoneID, added)
	}
}
