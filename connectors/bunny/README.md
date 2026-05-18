# connectors/bunny

Manage Bunny.net DNS zones and records via api.bunny.net. One pgf
instance = one Bunny account (one API key). Zones are referenced by
their numeric Bunny ID; records by the integer the API returns at
creation time.

## Actions

| Name | What it does |
|---|---|
| `list-zones` | Paginated list of zones on the account, with optional `search` filter. |
| `get-zone` | Full zone shape with embedded records. Records carry both string type names (A, CNAME, …) and Bunny's numeric type codes. |
| `create-zone` | Register a new domain as a Bunny DNS zone. |
| `delete-zone` | Permanently remove a zone and every record in it. |
| `check-zone-availability` | Verify a domain is available to add (no conflict with an existing zone). |
| `export-zone` | Dump a zone as BIND zone-file format. `out` is `IsPath: true` — subject to `PGF_ALLOWED_PATHS`. |
| `add-record` | Create a record. Pass `type` as the human-friendly string (A/AAAA/CNAME/MX/SRV/CAA/TXT/NS/…); the connector translates to Bunny's integer enum on the wire. |
| `update-record` | Partial update — only the fields you pass are changed. |
| `delete-record` | Remove a single record. |

## Credential

| Field | Required | Notes |
|---|---|---|
| `api_key` | yes | From https://dash.bunny.net/account/api-key. Sent as `AccessKey: <key>` header (NOT Bearer). |
| `api_base` | no | Default `https://api.bunny.net`. Override only for testing. |

`Validate` calls `GET /dnszone?perPage=5` — confirms auth + reachability
and reports how many zones the account holds.

## Record types

The Bunny API encodes record types as integers (0=A, 1=AAAA, …,
15=TLSA). The connector accepts the human-friendly string everywhere
on the CLI and surfaces both forms in responses (`"type": "A",
"type_code": 0`). The complete table:

| String | Code | Notes |
|---|---|---|
| `A` | 0 | IPv4 |
| `AAAA` | 1 | IPv6 |
| `CNAME` | 2 | Alias |
| `TXT` | 3 | Free-form text; safe for verification / SPF / DKIM |
| `MX` | 4 | Mail exchange (requires `priority`) |
| `Redirect` | 5 | Bunny-specific HTTP redirect |
| `Flatten` | 6 | Bunny-specific CNAME-at-apex |
| `PullZone` | 7 | Bunny CDN pullzone link |
| `SRV` | 8 | Service location (requires `priority`, `weight`, `port`) |
| `CAA` | 9 | Certificate Authority Authorization (requires `flags`, `tag`) |
| `PTR` | 10 | Reverse DNS |
| `Script` | 11 | Bunny edge script |
| `NS` | 12 | Nameserver delegation |
| `SVCB` | 13 | Service binding |
| `HTTPS` | 14 | HTTPS service binding |
| `TLSA` | 15 | DANE TLS association |

### Conditional fields

`add-record` uses `ShowWhen` to hide type-specific fields when they
don't apply:

- `priority` shows only for `MX` and `SRV`
- `weight` and `port` show only for `SRV`
- `flags` and `tag` show only for `CAA`

So `pgf connect bunny <name>` followed by `add-record` interactively
prompts only for what the chosen type needs. Same gating applies to
the path-whitelist validator: an irrelevant `priority` value on a TXT
record doesn't trip anything.

## Usage

```bash
# Setup
pgf connect bunny dash      # wizard; paste your key with no echo

# List zones
pgf run bunny/dash list-zones

# Inspect a zone (records embedded)
pgf run bunny/dash get-zone -p id=689859

# Add a TXT record
pgf run bunny/dash add-record \
  -p zone_id=794089 \
  -p type=TXT -p name=pgf-test \
  -p value="hello bunny" \
  -p ttl=300

# Add an MX record (priority is required)
pgf run bunny/dash add-record \
  -p zone_id=794089 \
  -p type=MX -p name=@ -p value=aspmx.l.google.com \
  -p priority=10 -p ttl=3600

# Update only the value (leave TTL alone)
pgf run bunny/dash update-record \
  -p zone_id=794089 -p record_id=17382037 \
  -p value="updated value"

# Delete
pgf run bunny/dash delete-record -p zone_id=794089 -p record_id=17382037

# Export zone as BIND
pgf run bunny/dash export-zone \
  -p id=794089 -p out=/tmp/dropjetzt.zone
```

## Live verification

The connector was fully verified end-to-end against a real account
(May 2026):

- `list-zones`, `check-zone-availability`, `get-zone` (with 65-record
  zone showing correct int→string type translation across A, TXT, MX)
- `add-record` + `update-record` + `delete-record` cycle on a TXT
  record in a real production zone with zero garbage left behind

## Known gaps

- **No `list-records` action.** Records are embedded in `get-zone`'s
  response, so a separate list endpoint would be redundant. Filter
  with `jq '.records[] | select(.type=="A")'`.
- **No DNSSEC management.** Bunny exposes DNSSEC key endpoints; not
  wrapped here yet (rarely changed by agents).
- **No statistics / monitoring config.** Bunny's per-record
  `MonitorType`, `Geolocation*`, `LatencyZone` fields are accepted
  through `raw=true` paths but not exposed as first-class params.
- **No zone import.** `POST /dnszone/{id}/import` (BIND file →
  records) isn't wrapped. Easy to add when needed.
- **No certificate issuance.** `POST /dnszone/{id}/certificate/issue`
  isn't wrapped — it's an edge-case operation for the wildcard cert
  flow.
