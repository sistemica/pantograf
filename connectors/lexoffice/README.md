# connectors/lexoffice

Lexware Office (formerly Lexoffice) connector. German accounting / invoicing
SaaS. Bearer-token REST + JSON. Reading actions in v0.1; write actions
(create invoice, upload voucher file) come next.

## Actions

| Name | Description |
|---|---|
| `get-profile` | Bound organisation: `companyName`, `organizationId`, `taxType`, `businessFeatures`. Cheap; used as Validate probe. |
| `list-contacts` | Search by email / name / number / customer / vendor. Paginated. |
| `get-contact` | One contact by id. |
| `list-vouchers` | Unified search via `/v1/voucherlist` over all voucher kinds (invoices, credit notes, vouchers, quotations, …). Filters: type / status / contact / date range. |
| `get-voucher` | Dispatcher: tries the type-specific endpoint (`/v1/invoices/{id}` etc.) and falls back to `/v1/vouchers/{id}` on 404. Resolves both API-created and manually-entered rows. |
| `download-voucher-pdf` | Resolves voucher → file id (via `/document` endpoint or `voucher.files[0]`) → streams `/v1/files/{id}` to disk as binary PDF. |

## Triggers

None yet. Lexware supports webhooks for `voucher.*`, `invoice.*` events;
plan to expose them either as a Lexware-specific WebhookTrigger or just as
a `set-webhook` action that points at a generic `webhook` connector
instance (the latter is the more pantograf-native pattern).

## Credential

| Field | Required | Default | Notes |
|---|---|---|---|
| `api_key` | yes | — | From Lexware → Einstellungen → Öffentliche API. `FieldSecret`, encrypted at rest. Sent as `Authorization: Bearer <key>`. |
| `api_base` | no | `https://api.lexware.io` | Default since the rebrand; legacy `api.lexoffice.io` was retired Dec 2025. |

`Validate` calls `/v1/profile` and prints the bound `companyName`.

## Voucher type → endpoint mapping

`/v1/voucherlist` is a unified view; each row's full record lives at a
different resource depending on origin. The dispatcher in `get-voucher` /
`download-voucher-pdf` handles this. Reference table:

| voucherType (from voucherlist) | Resource endpoint | Has `/document` rendered file? |
|---|---|---|
| `salesinvoice`, `invoice` | `/v1/invoices/{id}` | yes |
| `salescreditnote` | `/v1/credit-notes/{id}` | yes |
| `downpaymentinvoice` | `/v1/down-payment-invoices/{id}` | yes |
| `quotation` | `/v1/quotations/{id}` | yes |
| `orderconfirmation` | `/v1/order-confirmations/{id}` | yes |
| `deliverynote` | `/v1/delivery-notes/{id}` | yes |
| `dunning` | `/v1/dunnings/{id}` | yes |
| `purchaseinvoice`, `purchasecreditnote` | `/v1/vouchers/{id}` | no — uses `voucher.files[]` |

**Important:** a voucherlist row tagged `salesinvoice` may actually live at
`/v1/vouchers/{id}` if it was entered through the bookkeeping module
rather than the API or the Rechnungen UI. `get-voucher` falls back
automatically; the response includes `_source_endpoint` and (when fallback
fired) `_fell_back_from` for transparency.

## Rate limits

Lexware: **2 req/s** per token. Exceeding returns HTTP 429 with the
account blocked for "seconds to minutes". Repeated abuse can lead to
permanent blocking.

The connector wraps every HTTP call in `retryOn429` — exponential
backoff (600 ms → 1.2 s → 2.4 s → 4.8 s → 9.6 s, capped at 30 s) up to 5
attempts before giving up. Lexware does not document `Retry-After`, so
backoff is pure exponential.

## Usage

```bash
pgf connect lexoffice sistemica
# wizard: paste API key (sealed at rest) → /v1/profile probe

pgf run lexoffice/sistemica get-profile

# Find recent paid incoming invoices
pgf run lexoffice/sistemica list-vouchers \
  -p voucher_type=purchaseinvoice -p voucher_status=paid -p size=10

# Drill into one voucher (works regardless of origin)
pgf run lexoffice/sistemica get-voucher \
  -p id=<uuid> -p voucher_type=salesinvoice

# Pull the PDF
pgf run lexoffice/sistemica download-voucher-pdf \
  -p id=<uuid> -p voucher_type=salesinvoice -p out=/tmp/invoice.pdf
```

## What to expect on the wire

- `Authorization: Bearer <key>` is set per-connection, applied to every
  request.
- `Accept: application/json` is set ONLY for JSON-helper calls
  (GetJSON / PostJSON / PostForm / PostMultipart). Binary downloads via
  `Do()` deliberately leave Accept empty — Lexware returns base64-in-JSON
  if Accept is JSON, but native PDF bytes when Accept is unset. This was
  a real bug we hit during testing; the transport now keeps Accept out of
  the default header set so each helper opts in.
- Voucherlist pagination is Spring-style: `{ content[], totalElements,
  totalPages, number, size, sort, last, first }`.
- Empty / missing required filters return HTTP 400 with
  `{"message": "Missing required request parameters: [...]"}`. The
  `voucherStatus` field is always required; we default to `any`.

## Known gaps

- No write actions yet (create-invoice, create-voucher, upload-voucher-file).
  The Belege ingestion script could route through pantograf once these land.
- No webhook trigger wiring — configure manually via the Lexware UI for now.
- No support for `Retry-After` header parsing (Lexware doesn't document it).
- Pagination is exposed through `page` and `size`; no auto-paginate helper.
