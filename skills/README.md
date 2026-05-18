# pantograf skills

One skill file per connector. Drop them into your agent's skill
directory (e.g. `~/.claude/skills/`) so the agent knows when to reach
for `pgf` and which actions are available.

```
skills/
├── pgf-email/SKILL.md
├── pgf-telegram/SKILL.md
├── pgf-matrix/SKILL.md
├── pgf-webhook/SKILL.md
├── pgf-llm/SKILL.md
├── pgf-rss/SKILL.md
├── pgf-youtrack/SKILL.md
└── pgf-lexoffice/SKILL.md
```

## Why one skill per connector

- **Focused triggers.** Each skill's `description:` lists the kinds of
  user requests that route to that connector. Claude picks the right
  one without loading the others.
- **Bounded context.** Each file is small (the connector's action
  surface plus a few common patterns). No skill ships the whole pgf
  catalog at once.
- **Independent updates.** Adding `read-emails`'s new field doesn't
  touch the telegram skill.

## Install

Each skill is a self-contained directory containing `SKILL.md`. Symlink
or copy the directories you want active:

```bash
# Symlink — easy to pull updates with git
for d in skills/pgf-*; do
  ln -sf "$(pwd)/$d" ~/.claude/skills/
done

# Or copy
cp -r skills/pgf-* ~/.claude/skills/
```

## Prerequisite for every skill

`pgf` must be on `$PATH` and the connector's instance must be
provisioned. Each skill names the instance generically (e.g.
`email/<your-instance>`); the agent should ask the user (or check
`pgf instances`) for the actual name.

```bash
go install github.com/sistemica/pantograf/cmd/pgf@latest
pgf connect <type> <name>     # interactive wizard, once per credential
```

## Conventions across all skills

- Action names are kebab-case: `read-emails`, `send-message`,
  `chat-completion`.
- Params are passed via `-p key=value` or `--input '{...}'` for nested
  data. Lists: comma-separated (`-p to=a,b`) or repeated (`-p to=a -p to=b`).
- Every `pgf run` outputs JSON. Pipe through `jq` or read with the
  agent's JSON tools.
- Long-running triggers stream NDJSON to stdout via
  `pgf watch <type>/<name> <trigger>`.
