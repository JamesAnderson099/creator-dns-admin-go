package main

import "sort"

type CreatorDNS struct {
	AssetOrigin     string
	UpdateChannel   string
	ProcessorOrigin string
	TTL             int
}

type DNSRecord struct {
	RecordID string `json:"record_id,omitempty"`
	ZoneID   string `json:"zone_id,omitempty"`
	Type     string `json:"record_type"`
	Name     string `json:"name"`
	Content  string `json:"content"`
	TTL      int    `json:"ttl,omitempty"`
	Priority *int   `json:"priority,omitempty"`
	Proxied  *bool  `json:"proxied,omitempty"`
}

func desiredRecords(c CreatorDNS) []DNSRecord {
	return []DNSRecord{
		{Type: "CNAME", Name: "assets", Content: c.AssetOrigin, TTL: c.TTL},
		{Type: "TXT", Name: "_creator-updates", Content: "channel=" + c.UpdateChannel, TTL: c.TTL},
		{Type: "CNAME", Name: "process", Content: c.ProcessorOrigin, TTL: c.TTL},
	}
}

// planRecordUpserts is the business boundary: only absent or drifted records are written.
func planRecordUpserts(c CreatorDNS, current []DNSRecord) []DNSRecord {
	byKey := make(map[string]DNSRecord, len(current))
	for _, record := range current {
		byKey[record.Type+"\x00"+record.Name] = record
	}

	var changes []DNSRecord
	for _, wanted := range desiredRecords(c) {
		existing, found := byKey[wanted.Type+"\x00"+wanted.Name]
		if !found || existing.Content != wanted.Content || existing.TTL != wanted.TTL {
			wanted.RecordID = existing.RecordID
			changes = append(changes, wanted)
		}
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Name < changes[j].Name })
	return changes
}
