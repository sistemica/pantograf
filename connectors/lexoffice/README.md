# connectors/lexoffice

Lexware Office (formerly Lexoffice) connector. German accounting / invoicing
SaaS. Bearer-token REST + JSON. Read actions plus a write path (list posting
categories, create purchase-invoice vouchers, attach files) that mirrors the
`lx` CLI so vouchers book identically.

## Actions

| Name | Description |
|---|---|
| `get-profile` | Bound organisation: `companyName`, `organizationId`, `taxType`, `businessFeatures`. Cheap; used as Validate probe. |
| `list-contacts` | Search by email / name / number / customer / vendor. Paginated. |
| `get-contact` | One contact by id. |
| `list-categories` | Posting categories (`/v1/posting-categories`) — the `categoryId` values voucher items reference. Optional name substring + type filter (`income`/`outgo`/`receivable`/`payable`). |
| `list-vouchers` | Unified search via `/v1/voucherlist` over all voucher kinds (invoices, credit notes, vouchers, quotations, …). Filters: type / status / contact / date range. |
| `get-voucher` | Dispatcher: tries the type-specific endpoint (`/v1/invoices/{id}` etc.) and falls back to `/v1/vouchers/{id}` on 404. Resolves both API-created and manually-entered rows. |
| `download-voucher-pdf` | Resolves voucher → file id (via `/document` endpoint or `voucher.files[0]`) → streams `/v1/files/{id}` to disk as binary PDF. |
| `create-purchase-voucher` | Create a `purchaseinvoice` voucher (Eingangsrechnung) via `POST /v1/vouchers`, optionally attaching a PDF in the same call. Net/tax math + §13b reverse charge — see below. |
| `attach-voucher-file` | Attach a file to an existing voucher (`POST /v1/vouchers/{id}/files`). Returns the new file id. |

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

## Creating purchase vouchers

`create-purchase-voucher` mirrors `lx voucher create-purchase`. Net/tax math
(rate defaults to 19%):

- give `net` → `tax = net * rate/100`, `gross = net + tax`
- give `gross` → `net = gross / (1 + rate/100)`, `tax = gross - net`
- give both → used as-is; `tax` always overrides the computed tax
- `reverse_charge=true` books **§13b**: `gross == net`, `tax 0`, `taxType
  gross` — pass the §13b / foreign-service `category_id`; Lexware derives the
  reverse-charge entries from the category + rate.

All amounts are decimal-string params (e.g. `-p net=1230.00`); they're rounded
to cents with the same rounder `lx` uses (see the `round2` quirk note in
`write.go`). Amounts book to identical values either way.

```bash
# Resolve a posting category id
pgf run lexoffice/sistemica list-categories -p search=Fremdleistungen -p type=outgo

# Create an incoming invoice and attach its PDF in one call
pgf run lexoffice/sistemica create-purchase-voucher \
  -p contact_id=<vendor-uuid> -p category_id=<cat-uuid> \
  -p number=2026-011 -p date=2026-03-09 -p due=2026-04-20 \
  -p net=1230 -p rate=19 -p remark="Rg 2026-011" \
  -p pdf=/path/to/invoice.pdf

# Attach a file to an existing voucher
pgf run lexoffice/sistemica attach-voucher-file -p voucher_id=<uuid> -p file=/path/to/x.pdf
```

> **Upload content-type quirk:** the file part is sent as
> `application/octet-stream`, which Lexware accepts (Paperless does not —
> see `write.go`). The body is buffered so `Content-Length` is set, matching
> the `lx` CLI byte-for-byte.

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

- No **sales**-invoice creation yet — only `purchaseinvoice` vouchers
  (`create-purchase-voucher`). Creating `/v1/invoices` (Rechnungen) with line
  items / customer rendering is not wrapped.
- No voucher **update / delete** — create + attach only.
- No webhook trigger wiring — configure manually via the Lexware UI for now.
- No support for `Retry-After` header parsing (Lexware doesn't document it).
- Pagination is exposed through `page` and `size`; no auto-paginate helper.
