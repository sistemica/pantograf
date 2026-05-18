#!/usr/bin/env bash
# send.sh — outbound email composed through a vetted template, never
# a raw LLM body. The LLM fills designated slots (the body paragraph);
# the surrounding template (greeting, signature, legal lines) is fixed.
#
# Usage:
#   send.sh <recipient_email> <recipient_name> "<topic>" "<context>"
#
# Example:
#   send.sh jane@example.com "Jane" \
#     "Follow-up on last week's call" \
#     "We discussed migrating their CMS. They asked for a timeline next week."

set -euo pipefail

TO="${1:?usage: send.sh <to> <name> <topic> <context>}"
NAME="${2:?missing recipient name}"
TOPIC="${3:?missing topic}"
CONTEXT="${4:?missing context}"

# ── configuration ─────────────────────────────────────────────────────
EMAIL_INST="email/inbox"
LLM_INST="llm/local"
MODEL="qwen36-27b"
TEMPLATE="$(dirname "$(realpath "$0")")/../templates/email-followup.md"

SIGNER="${SIGNER:-Jane Doe}"
COMPANY="${COMPANY:-Acme Consulting}"

[ -f "$TEMPLATE" ] || { echo "template missing: $TEMPLATE" >&2; exit 1; }

# ── LLM drafts ONLY the body paragraph ────────────────────────────────
echo "── LLM is drafting the email body (template-bounded)"

BODY_RESP=$(pgf run "$LLM_INST" chat-completion \
  -p model="$MODEL" \
  -p system="You write the BODY of a follow-up email — one to three short paragraphs in plain prose. Do NOT include greetings ('Hi $NAME'), signoffs ('Best regards'), or signature lines — those are added by the template. Do not use markdown formatting. Output only the body text." \
  -p prompt="Topic: $TOPIC

Context: $CONTEXT

Write the body of a polite, concise follow-up email to $NAME.")

BODY=$(echo "$BODY_RESP" | jq -r .text)

if [ -z "$BODY" ] || [ "$BODY" = "null" ]; then
  echo "LLM returned no body" >&2
  exit 1
fi

# ── render the template ──────────────────────────────────────────────
# Use a temp file with envsubst-style substitution. Each placeholder is
# replaced exactly once; the surrounding template (signature, legal
# block) is verbatim from the file on disk.
RENDERED=$(awk \
  -v NAME="$NAME" \
  -v TOPIC="$TOPIC" \
  -v BODY="$BODY" \
  -v SIGNER="$SIGNER" \
  -v COMPANY="$COMPANY" \
  '!/^<!--/ && !/^-->/ {
     gsub(/\{\{NAME\}\}/,    NAME);
     gsub(/\{\{TOPIC\}\}/,   TOPIC);
     gsub(/\{\{BODY\}\}/,    BODY);
     gsub(/\{\{SIGNER\}\}/,  SIGNER);
     gsub(/\{\{COMPANY\}\}/, COMPANY);
     print
   }' "$TEMPLATE")

# Skip the HTML comment header (lines before the first non-comment).
# The awk above already filters comment lines; re-trim leading blanks.
RENDERED=$(echo "$RENDERED" | sed '/./,$!d')

# ── refuse to send if any placeholder remains unfilled ────────────────
if echo "$RENDERED" | grep -qE '\{\{[A-Z_]+\}\}'; then
  echo "Refusing to send: template has unfilled placeholders" >&2
  echo "$RENDERED" | grep -E '\{\{[A-Z_]+\}\}' >&2
  exit 1
fi

echo "── PREVIEW ──────────────────────────────────────────────"
echo "$RENDERED"
echo "── END PREVIEW ──────────────────────────────────────────"

read -r -p "Send to $TO? [y/N] " ANS
case "$ANS" in
  y|Y|yes) ;;
  *) echo "aborted"; exit 0 ;;
esac

pgf run "$EMAIL_INST" send-email \
  -p to="$TO" \
  -p subject="$TOPIC" \
  -p body="$RENDERED" \
  | jq -c '{sent, to}'
