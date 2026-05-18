// Pantograf example: invoice template.
//
// Numbers come from your accounting system (e.g. Lexware via the
// pgf lexoffice connector), NOT from the LLM. The LLM at most drafts
// a cover note. This template enforces the layout; data is injected
// via `typst compile --input data=<json>`.
//
// Expected JSON shape:
//   {
//     "invoice_number": str, "date": "YYYY-MM-DD", "due_date": "YYYY-MM-DD",
//     "client": { "name": str, "address": str, "vat_id": str },
//     "vendor": { "name": str, "address": str, "vat_id": str, "iban": str },
//     "items": [{ "description": str, "qty": num, "unit_price": num, "total": num }],
//     "subtotal": num, "vat_rate": num, "vat_amount": num, "total": num,
//     "notes": str
//   }

// Data passed as inline JSON string via `--input data='{...}'`.
#let data = json.decode(sys.inputs.data)

#set page(margin: 2cm)
#set text(size: 10pt)

// ── header ────────────────────────────────────────────────────────────
#grid(columns: (1fr, 1fr),
  align: (left, right),
  [*#data.vendor.name* \ #data.vendor.address],
  [Invoice ##data.invoice_number \ Date: #data.date \ Due: #data.due_date],
)

#v(1cm)

// ── client block ──────────────────────────────────────────────────────
*Bill to:* \
#data.client.name \
#data.client.address \
#if data.client.vat_id != none [VAT ID: #data.client.vat_id]

#v(1cm)

// ── line items ────────────────────────────────────────────────────────
#table(
  columns: (1fr, auto, auto, auto),
  align: (left, right, right, right),
  stroke: 0.4pt,
  table.header(
    [*Description*], [*Qty*], [*Unit price*], [*Line total*],
  ),
  ..for item in data.items {
    (item.description, str(item.qty), str(item.unit_price), str(item.total))
  }
)

#v(0.5cm)

// ── totals ────────────────────────────────────────────────────────────
#align(right)[
  #table(
    columns: (auto, auto),
    align: (right, right),
    stroke: none,
    [Subtotal], [#data.subtotal],
    [VAT (#data.vat_rate%)], [#data.vat_amount],
    [*Total*], [*#data.total*],
  )
]

#v(1cm)

#if data.notes != none and data.notes != "" [
  *Notes* \
  #data.notes
]

#v(2cm)

// ── footer ────────────────────────────────────────────────────────────
#align(center)[
  #text(size: 8pt)[
    #data.vendor.name · VAT #data.vendor.vat_id · IBAN #data.vendor.iban
  ]
]
