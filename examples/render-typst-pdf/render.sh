#!/usr/bin/env bash
# render.sh — generic mechanism for filling a typst template with JSON
# and rendering a PDF.
#
# Usage:
#   render.sh <template.typ> <data.json> <output.pdf>
#
# The template MUST read its data via `sys.inputs.data` and parse it
# with `json()`. See examples/templates/proposal.typ for the shape.

set -euo pipefail

TEMPLATE="${1:?usage: render.sh <template.typ> <data.json> <output.pdf>}"
DATA="${2:?missing data.json}"
OUTPUT="${3:?missing output.pdf}"

[ -f "$TEMPLATE" ] || { echo "template not found: $TEMPLATE" >&2; exit 1; }
[ -f "$DATA" ]     || { echo "data not found: $DATA"         >&2; exit 1; }

# Validate JSON first — fail loud before invoking typst.
jq empty "$DATA" || { echo "data is not valid JSON: $DATA" >&2; exit 1; }

# typst's `--input key=value` exposes inputs at `sys.inputs.<key>`.
# Pass the JSON CONTENT inline (compact, single-line) so the template
# can `json.decode(sys.inputs.data)` without typst's sandbox blocking
# file reads outside the project root.
typst compile --input "data=$(jq -c . "$DATA")" "$TEMPLATE" "$OUTPUT"

echo "rendered → $OUTPUT"
