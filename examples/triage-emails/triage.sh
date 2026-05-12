#!/usr/bin/env bash
# triage-emails — pantograf workflow example
#
# For each recent email in the configured inbox:
#   1. Ask the LLM whether it looks like a bug report (YES/NO)
#   2. If YES, file a YouTrack issue with the email's subject + body
#
# Demonstrates: composing three pgf connectors (email, llm, youtrack)
# in plain bash + jq. No DSL needed — pgf actions read stdin and write
# JSON, so the shell is enough for sequential workflows.
#
# Prerequisites:
#   pgf connect email <instance>          # IMAP+SMTP credentials
#   pgf connect llm   <instance>          # OpenAI-compatible endpoint
#   pgf connect youtrack <instance>       # permanent token
#
# Configure the instance names + project below, then run on a schedule
# (cron, systemd timer, or whatever).

set -euo pipefail

# ── configuration ─────────────────────────────────────────────────────────
EMAIL_INST="email/inbox"          # pgf email instance to read from
LLM_INST="llm/local"              # pgf llm instance to classify with
YT_INST="youtrack/main"           # pgf youtrack instance to file in
MODEL="qwen36-27b"                # any model id available on LLM_INST
YT_PROJECT="DEMO"                 # YouTrack project shortName
INBOX_FOLDER="INBOX"
LIMIT=10                          # how many recent emails to consider

# ── workflow ──────────────────────────────────────────────────────────────
echo "── reading last $LIMIT emails from $EMAIL_INST"

pgf run "$EMAIL_INST" read-emails \
  -p folder="$INBOX_FOLDER" -p limit="$LIMIT" \
  | jq -c '.[]' \
  | while IFS= read -r email; do

  SUBJ=$(echo "$email" | jq -r .subject)
  FROM=$(echo "$email" | jq -r .from)
  BODY=$(echo "$email" | jq -r .body)

  printf "\n[email] %s — %s\n" "$FROM" "$SUBJ"

  # ── classify ─────────────────────────────────────────────────────────
  CLASSIFY=$(pgf run "$LLM_INST" chat-completion \
    -p model="$MODEL" \
    -p prompt="Is the following email a bug report? Answer with exactly one word: YES or NO.

From: $FROM
Subject: $SUBJ
Body: $BODY")

  # Normalize: first 3 chars, uppercase, no whitespace
  ANSWER=$(echo "$CLASSIFY" | jq -r .text | tr -d '[:space:]' | tr '[:lower:]' '[:upper:]' | cut -c1-3)
  printf "  classified: %s\n" "$ANSWER"

  # ── act ──────────────────────────────────────────────────────────────
  if [ "$ANSWER" = "YES" ]; then
    ISSUE=$(pgf run "$YT_INST" create-issue \
      -p project_key="$YT_PROJECT" \
      -p summary="$SUBJ" \
      -p description="Auto-filed by triage from $FROM

$BODY")
    NEW_ID=$(echo "$ISSUE" | jq -r .idReadable)
    printf "  → filed %s\n" "$NEW_ID"
  else
    printf "  → skipped\n"
  fi
done
