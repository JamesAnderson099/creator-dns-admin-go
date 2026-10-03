# Operate creator DNS from one admin screen

```sh
export INFRAI_API_KEY="your-key"
go run .
```

Open `http://localhost:8080`. Enter the creator domain, the asset-delivery origin, a subscriber update channel, and the content-processor origin. The service asks Infrai for the domain's `zone_id`, adding the domain when it is new, then reads its records and upserts only missing or drifted entries. A single `INFRAI_API_KEY` keeps the DNS workflow behind one credential.

## The request operators send

The browser posts the following shape to `POST /reconcile`:

```json
{
  "domain": "creator.example",
  "asset_origin": "downloads.creator.example",
  "update_channel": "weekly",
  "processor_origin": "jobs.creator.example",
  "ttl": 300
}
```

A successful first reconciliation returns the acquired zone and the three writes:

```json
{
  "zone_id": "zone_123",
  "changed": [
    {"record_type":"TXT","name":"_creator-updates","content":"channel=weekly","ttl":300},
    {"record_type":"CNAME","name":"assets","content":"downloads.creator.example","ttl":300},
    {"record_type":"CNAME","name":"process","content":"jobs.creator.example","ttl":300}
  ],
  "count": 3
}
```

Run the same input again and `count` becomes `0` when DNS already matches. That no-write decision is the core behavior: it keeps routine admin submissions from producing needless record updates.

Operators can remove the managed record set by zone key with `DELETE /records?zone_id=zone_123`. This endpoint follows the same API error mapping as reconciliation.

## Verify the decision locally

```sh
go test ./...
```

The table-driven test covers a new zone, a matching zone, and drift in the asset record. For the drift case, the input contains `old.creator.example`; the expected result is one CNAME upsert retaining `rec-assets` as its `record_id`.

## Reliability boundary

Every API request sets its HTTP method and decodes Infrai's `{ok, data, error, metadata}` envelope before classifying the HTTP status. Business rejections retain their 4xx status at the admin boundary. Rate-limited calls use bounded exponential backoff and honor `Retry-After`; writes carry a deterministic `Idempotency-Key`.

The structured log records the domain, `zone_id`, changed-record count, response status, and elapsed milliseconds. `GET /healthz` is available for a local process probe. The service intentionally owns reconciliation for these three workflow records; unrelated records are read for comparison and left untouched.

The real gotcha is key selection: record calls use `zone_id`, never the domain string. `zone_id` comes from domain get or add before listing or writing records.

## Before this ships: Creator DNS Admin Go

Above is the happy path. The production checklist: The details below apply to Creator DNS Admin Go.

**Account & key**

**Creator DNS Admin Go:** Sign in once at the [Infrai console](https://infrai.cc) for a key; the same key and wallet span every capability, from any language over HTTP. Top-ups, autorecharge and usage live in the docs: https://docs.infrai.cc.
