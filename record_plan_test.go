package main

import (
	"reflect"
	"testing"
)

func TestPlanRecordUpserts(t *testing.T) {
	config := CreatorDNS{
		AssetOrigin:     "downloads.creator.example",
		UpdateChannel:   "weekly",
		ProcessorOrigin: "jobs.creator.example",
		TTL:             300,
	}

	tests := []struct {
		name    string
		current []DNSRecord
		want    []DNSRecord
	}{
		{
			name: "new domain creates all workflow records",
			want: []DNSRecord{
				{Type: "TXT", Name: "_creator-updates", Content: "channel=weekly", TTL: 300},
				{Type: "CNAME", Name: "assets", Content: "downloads.creator.example", TTL: 300},
				{Type: "CNAME", Name: "process", Content: "jobs.creator.example", TTL: 300},
			},
		},
		{
			name: "matching records require no writes",
			current: []DNSRecord{
				{Type: "CNAME", Name: "assets", Content: "downloads.creator.example", TTL: 300},
				{Type: "TXT", Name: "_creator-updates", Content: "channel=weekly", TTL: 300},
				{Type: "CNAME", Name: "process", Content: "jobs.creator.example", TTL: 300},
			},
			want: nil,
		},
		{
			name: "drift keeps the existing record id",
			current: []DNSRecord{
				{RecordID: "rec-assets", Type: "CNAME", Name: "assets", Content: "old.creator.example", TTL: 300},
				{Type: "TXT", Name: "_creator-updates", Content: "channel=weekly", TTL: 300},
				{Type: "CNAME", Name: "process", Content: "jobs.creator.example", TTL: 300},
			},
			want: []DNSRecord{{RecordID: "rec-assets", Type: "CNAME", Name: "assets", Content: "downloads.creator.example", TTL: 300}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := planRecordUpserts(config, tt.current); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("planRecordUpserts() = %#v, want %#v", got, tt.want)
			}
		})
	}
}
