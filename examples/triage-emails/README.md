# triage-emails

A pantograf workflow example that demonstrates composing three connectors
in plain bash:

```
read inbox  →  LLM classify ("bug report? YES/NO")  →  file YouTrack issue if YES
```

No DSL, no scheduler, no orchestrator — pgf actions return JSON, jq
pulls fields out, the shell loops. ~40 lines, runs from cron.

## What you'll need

| Connector | Instance you create | Purpose |
|---|---|---|
| `email` | `email/inbox` (or any name) | IMAP + SMTP — the inbox to read |
| `llm` | `llm/local` | OpenAI-compatible chat endpoint |
| `youtrack` | `youtrack/main` | A permanent token with create-issue rights |

Update the constants at the top of `triage.sh` to match your instance
names and the YouTrack project shortName.

## Setup

```bash
# 1. Build pgf
go install github.com/sistemica/pantograf/cmd/pgf@latest

# 2. Provision each instance interactively
pgf connect email inbox       # wizard prompts for IMAP/SMTP host + creds
pgf connect llm local         # base URL + API key (e.g. local proxy, OpenAI)
pgf connect youtrack main     # YouTrack permanent token + base URL

# 3. Edit triage.sh to set EMAIL_INST / LLM_INST / YT_INST / YT_PROJECT
$EDITOR examples/triage-emails/triage.sh

# 4. Try it
./examples/triage-emails/triage.sh
```

Or non-interactively (good for scripts / containers):

```bash
pgf connect --input '{
  "email": "you@example.com",
  "password": "...",
  "imap_host": "imap.example.com", "imap_port": 993, "imap_security": "tls",
  "smtp_host": "smtp.example.com", "smtp_port": 465, "smtp_security": "tls"
}' email inbox

pgf connect --input '{
  "api_key": "sk-...",
  "api_base": "http://your-llm-host:4000/v1"
}' llm local

pgf connect --input '{
  "token": "perm-...",
  "base_url": "https://your-youtrack.example.com"
}' youtrack main
```

## Schedule

Crontab — every 5 minutes:

```cron
*/5 * * * * /path/to/triage.sh >> /var/log/triage.log 2>&1
```

systemd timer is just as good. The script is idempotent only in the
sense that already-filed bugs won't be re-classified differently — but
running it twice over the same email **will** file two YouTrack issues.
For real production, persist a "last-processed UID" somewhere and skip
emails ≤ that watermark. (Or wait for the upcoming IMAP IDLE trigger
on the email connector — then it becomes "watch one stream of new
arrivals" with no UID bookkeeping.)

## What it teaches

Looking at the script you'll see how three different connectors plug
into the same call pattern:

```bash
pgf run <instance> <action> -p key=value ... | jq ...
```

Every action returns JSON. Every action is callable from any shell.
Same shape for email, LLM, issue tracker — and for every connector that
ships with pantograf or that you build yourself.

## When this stops being enough

This is fine for sequential, single-step-fanout flows. When you start
needing:

- Parallel branches
- Retry policies with backoff
- Cross-step state that's more than env vars
- Resume-after-crash durability
- Many flows that share boilerplate

…the right answer is probably a real workflow engine (Temporal, Argo,
Camel, or a future pantograf YAML DSL). For "read X, decide, do Y",
bash is exactly the right tool.
