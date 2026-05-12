# pantograf examples

Working workflows composed from the bundled connectors. Each example is
self-contained and runnable once you've connected the instances it needs.

| Example | Connectors used | What it does |
|---|---|---|
| [triage-emails](triage-emails/) | email + llm + youtrack | reads inbox, asks the LLM if each email is a bug report, files matching ones as YouTrack issues |

Pattern: pgf actions return JSON to stdout. Compose them with shell +
jq for sequential workflows. Reach for a workflow engine (Temporal,
Camel, Argo) only when you need real parallelism, retries, or
persistence beyond what bash gives you.

## Contributing an example

Drop a directory in here with:

- `<name>.sh` (or any runnable script — Python, Go, whatever)
- `README.md` describing prerequisites, setup, and what it demonstrates
- No hardcoded credentials, real domain names, or organization-specific
  identifiers. Use placeholders like `email/inbox`, `your-host`, `DEMO`
  as the YouTrack project. Examples are documentation — keep them
  portable.
