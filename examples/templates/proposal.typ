// Pantograf example: minimal proposal template.
// Filled by the LLM via JSON injected through `typst compile --input data=<path>`.
//
// Expected JSON shape (see examples/draft-proposal/):
//   {
//     "title": str, "client": str, "date": "YYYY-MM-DD",
//     "summary": str, "scope": [str, ...],
//     "deliverables": [str, ...], "pricing": str, "timeline": str,
//     "author": str, "company": str, "contact": str
//   }

// `data` is passed as a JSON string via `--input data='{...}'`
// (typst v0.14 sandboxes file reads outside the project root, so the
// inline-string approach avoids needing `--root` configuration).
#let data = json.decode(sys.inputs.data)

#set page(margin: 2cm, numbering: "1/1")
#set text(size: 11pt)
#set par(justify: true, leading: 0.7em)

// ── header ────────────────────────────────────────────────────────────
#grid(columns: (1fr, auto),
  align: (left, right),
  [*#data.company*],
  [#data.date],
)

#v(1.5cm)

// ── title block ───────────────────────────────────────────────────────
#align(center)[
  #text(size: 18pt, weight: "bold")[Proposal]
  #v(0.5em)
  #text(size: 14pt)[#data.title]
  #v(0.5em)
  #text(style: "italic")[Prepared for #data.client]
]

#v(1cm)

// ── sections ──────────────────────────────────────────────────────────
== Executive summary
#data.summary

== Scope
#for item in data.scope [
  - #item
]

== Deliverables
#for item in data.deliverables [
  - #item
]

== Pricing
#data.pricing

== Timeline
#data.timeline

#v(2cm)
#line(length: 6cm)
#data.author \
#data.company \
#data.contact
