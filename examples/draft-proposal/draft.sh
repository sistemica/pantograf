#!/usr/bin/env bash
# draft.sh — turn a one-line brief into a PDF proposal.
#
#   brief → LLM (fills proposal.typ slots as JSON) → render → PDF
#
# Usage:
#   draft.sh "<brief>" <output.pdf>
#
# The LLM does NOT generate the template — only fills slots. The
# proposal layout, signature block, and any boilerplate live in
# examples/templates/proposal.typ and are immutable per run.

set -euo pipefail

BRIEF="${1:?usage: draft.sh \"<brief>\" <output.pdf>}"
OUTPUT="${2:?missing output.pdf}"

# ── configuration ─────────────────────────────────────────────────────
LLM_INST="${PGF_LLM_INSTANCE:-llm/local}"
MODEL="${PGF_LLM_MODEL:-qwen36-27b}"
TEMPLATE="$(dirname "$(realpath "$0")")/../templates/proposal.typ"
SCRIPT_DIR="$(dirname "$(realpath "$0")")"

# Boilerplate the LLM must respect — passed through, not regenerated.
AUTHOR="${PROPOSAL_AUTHOR:-Jane Doe}"
COMPANY="${PROPOSAL_COMPANY:-Acme Consulting}"
CONTACT="${PROPOSAL_CONTACT:-jane@acme.example}"

# ── ask the LLM for structured fields ────────────────────────────────
echo "── LLM is drafting proposal sections (model=$MODEL)"

RAW=$(pgf run "$LLM_INST" chat-completion \
  -p model="$MODEL" \
  -p system="You generate proposal content as a single JSON object. Output ONLY the JSON — no prose, no markdown fences. Use the exact keys: title, client, date, summary, scope, deliverables, pricing, timeline. scope and deliverables are arrays of short strings. date is ISO YYYY-MM-DD." \
  -p prompt="Brief: $BRIEF

Return JSON with these keys filled from the brief. Today is $(date +%Y-%m-%d). Be specific and concise — no fluff.")

TEXT=$(echo "$RAW" | jq -r .text)

# Strip a code fence if the model wrapped its JSON despite being told not to.
TEXT=$(echo "$TEXT" | sed -E 's/^```(json)?//; s/```$//')

# ── splice in the fixed boilerplate the template requires ────────────
DATA=$(echo "$TEXT" | jq \
  --arg author "$AUTHOR" \
  --arg company "$COMPANY" \
  --arg contact "$CONTACT" \
  '. + {author: $author, company: $company, contact: $contact}')

# Validate before invoking typst.
if ! echo "$DATA" | jq -e '.title and .client and .summary and (.scope|length>0) and (.deliverables|length>0)' >/dev/null; then
  echo "LLM output missing required fields. Got:" >&2
  echo "$DATA" | jq . >&2
  exit 1
fi

echo "$DATA" > /tmp/proposal-data.json
echo "── rendering PDF"
"$SCRIPT_DIR/../render-typst-pdf/render.sh" "$TEMPLATE" /tmp/proposal-data.json "$OUTPUT"
