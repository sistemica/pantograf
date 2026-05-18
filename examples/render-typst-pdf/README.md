# render-typst-pdf

The generic mechanism the other "drafted documents" examples build on:

```
template.typ + data.json → typst compile → PDF
```

`render.sh` does:
1. Validate the data is JSON.
2. Run `typst compile --input data=<path> template.typ output.pdf`.
3. The template reads its data with `let data = json(sys.inputs.data)`.

## Use it

```bash
echo '{
  "title": "Quick demo",
  "client": "Acme Corp",
  "date": "2026-05-08",
  "summary": "...",
  "scope": ["item 1", "item 2"],
  "deliverables": ["a", "b"],
  "pricing": "Fixed-fee 10,000 EUR.",
  "timeline": "4 weeks.",
  "author": "Jane",
  "company": "Acme Consulting",
  "contact": "jane@acme.example"
}' > /tmp/proposal-data.json

./render.sh ../templates/proposal.typ /tmp/proposal-data.json /tmp/proposal.pdf
```

## Why this exists

To pin "you can drop any JSON into any typst template" as the lowest
layer of doc rendering, and to make it dead simple to swap templates
without changing the script. The `draft-proposal` example layers an
LLM step in front of `render.sh`.

## Prereqs

- [typst](https://typst.app) on `$PATH` (`typst --version`)
- `jq` for the validation step
