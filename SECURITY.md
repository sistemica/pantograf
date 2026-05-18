# pantograf Security Model

This document covers the threat model pantograf assumes, the defences it
provides, and a recommended deployment for hosting connectors behind an
AI agent.

If you only run pgf locally as your own user (CLI / library use), most of
this is overkill. Skip to **Threat Model** to decide what applies.

## Goals

pgf has two stated security goals:

1. **AI agents never touch credentials.** A connector instance is named
   (`email/work`, `telegram/personal`). Agents reference it by name; the
   secret values live in `~/.config/pgf/instances/<type>/<name>.yaml`,
   field-level encrypted (NaCl secretbox) using a master key at
   `~/.config/pgf/master.key`. There is no API to print secrets — the
   only way out is through a connector action.

2. **Agents can't ask connectors to read or write arbitrary files.**
   Several actions take filesystem paths (`send-email -p attachments=`,
   `download-attachment -p out=`, `download-voucher-pdf -p out=`,
   `webhook` credential `response_file`). Without enforcement, an agent
   asking `send-email` to attach `~/.config/pgf/master.key` would leak
   the very key that protects every other secret. The path whitelist
   (`PGF_ALLOWED_PATHS`) closes this.

## Threat Model

Assumed attacker: a compromised or simply mis-aligned AI agent that has
been granted the ability to invoke `pgf`. The agent can:

- run any `pgf` subcommand (connectors, actions, triggers)
- read its own stdin/stdout/stderr
- write files anywhere its uid permits

The agent **cannot**, in the secure deployment below:

- read `~pgf/.config/pgf/master.key` or any sealed YAML directly
- coerce `pgf` into reading or writing files outside the whitelisted
  roots
- escalate to another user without sudo policy

Out of scope: a fully compromised root user; physical access; a malicious
upstream connector library; supply-chain attacks on `go.sum`.

## Defences in Layers

### 1. Credentials are field-encrypted at rest

Fields declared `Kind: FieldSecret` in a connector's `CredentialSpec` are
sealed with `sealed:` + base64(NaCl secretbox). The master key is a
32-byte random value generated on first run, stored at
`~/.config/pgf/master.key` with mode `0600`. Two env-var overrides exist:

- `PGF_KEY_DIR` — alternate directory for `master.key`
- `PGF_MASTER_KEY` — base64-encoded 32-byte key supplied directly
  (useful for systemd `LoadCredentialEncrypted=` or sealed-secrets flows
  in K8s; bypasses on-disk persistence)

A leaked YAML file alone is not enough to recover plaintext secrets.

### 2. Path whitelist (`PGF_ALLOWED_PATHS`)

`PGF_ALLOWED_PATHS` is a colon-separated list of absolute roots. When
set, every credential or action field declared `IsPath: true` is checked
before the action runs. The check:

- resolves each path with `filepath.Abs`
- follows symlinks via `filepath.EvalSymlinks` (so a symlink inside an
  allowed root that points to a forbidden file is rejected against the
  real target)
- requires the resolved path to equal an allowed root or sit beneath it
  with a `/` boundary (`/var/lib/pgf-out` does not match
  `/var/lib/pgf-outsider`)

When **unset**, no enforcement is performed — local CLI use is
unchanged. The whitelist is an opt-in tightening for hosted / agent
deployments.

Fields currently flagged `IsPath: true`:

| Connector  | Field                                         | Where      |
| ---------- | --------------------------------------------- | ---------- |
| email      | `attachments` (send-email, save-draft)         | action     |
| email      | `out` (download-attachment)                    | action     |
| youtrack   | `file` (attach-file), `out` (download-attachment) | action  |
| lexoffice  | `out` (download-voucher-pdf)                   | action     |
| webhook    | `response_file`                                | credential |

Validation runs in two places (see `cmd/pgf/main.go`):

- `cmdConnect`: after `Validate()`, before `Put()` — rejects an attempt
  to set a credential field outside the allow-list.
- `cmdRun`: after `store.Get()` + secret decryption, before
  `sess.Open()` — rejects an attempt to invoke an action with a
  forbidden path parameter.

Error format names the field, the offending value, and the active roots:

```
field "attachments" value "/home/pgf/.config/pgf/master.key" is outside
PGF_ALLOWED_PATHS (/var/lib/pgf-out)
```

### 3. Filesystem permissions

| Path                                       | Mode  | Purpose                  |
| ------------------------------------------ | ----- | ------------------------ |
| `~/.config/pgf/master.key`                 | 0600  | Encryption key           |
| `~/.config/pgf/instances/`                 | 0700  | Sealed credential YAMLs  |
| `~/.config/pgf/instances/<type>/`          | 0700  | Per-type sub-dir         |
| `~/.config/pgf/instances/<type>/<name>.yaml` | 0600 | One credential          |
| `~/.local/state/pgf/state/`                | 0700  | Per-instance state       |

All filesystem code creates parent directories with restrictive modes
and writes files atomically via `tmp + rename`.

## Recommended Deployment: agent + pgf user split

When pantograf hosts connectors for an AI agent, run pgf as a **separate
Unix user** from the agent. The agent has no read access to the pgf
user's home directory.

```
                  ┌──────────────────────────┐
                  │ unprivileged user        │
                  │ "agent"                  │
                  │   • runs the model loop  │
                  │   • shells out to pgf    │
                  │     only via sudoers     │
                  └────────────┬─────────────┘
                               │  sudo -n -u pgf /usr/local/bin/pgf …
                               ▼
                  ┌──────────────────────────┐
                  │ "pgf" user                                       │
                  │   • ~/.config/pgf/ (master.key, sealed YAMLs)    │
                  │   • PGF_ALLOWED_PATHS=/var/lib/pgf-out           │
                  │   • shares /var/lib/pgf-out RW with agent group  │
                  └──────────────────────────────────────────────────┘
```

### Setup

```bash
# 1. Create the pgf user.
sudo useradd --system --create-home --shell /usr/sbin/nologin pgf

# 2. Install the binary.
sudo install -m 0755 ./pgf /usr/local/bin/pgf

# 3. Bootstrap credentials AS THE pgf USER (interactive).
sudo -i -u pgf
pgf connect email work
pgf connect telegram personal
# ... master.key is created at ~/.config/pgf/master.key, mode 0600.
exit

# 4. Create the shared drop-zone the agent and pgf both touch.
sudo mkdir -p /var/lib/pgf-out
sudo chown pgf:agent /var/lib/pgf-out
sudo chmod 2770 /var/lib/pgf-out   # setgid so new files inherit the group

# 5. Sudoers rule (NOPASSWD, scoped).
sudo visudo -f /etc/sudoers.d/pgf
```

`/etc/sudoers.d/pgf`:

```
# Allow the agent user to invoke pgf as the pgf user only.
# PGF_ALLOWED_PATHS is set in the env_keep envelope below.
Defaults:agent  env_keep += "PGF_ALLOWED_PATHS"
agent  ALL=(pgf) NOPASSWD: /usr/local/bin/pgf
```

### Per-shell or systemd env

Set `PGF_ALLOWED_PATHS` in the agent's environment (the sudoers rule
above preserves it through the sudo call):

```bash
# In the agent process's environment:
export PGF_ALLOWED_PATHS=/var/lib/pgf-out
```

For systemd-managed agents, prefer:

```
[Service]
User=agent
Environment=PGF_ALLOWED_PATHS=/var/lib/pgf-out
```

### What this gives you

| Capability                                                  | agent uid | pgf uid |
| ----------------------------------------------------------- | --------- | ------- |
| Read `~pgf/.config/pgf/master.key`                          | ❌        | ✅      |
| Read `~pgf/.config/pgf/instances/*.yaml`                    | ❌        | ✅      |
| Invoke `pgf run …` (via sudo)                               | ✅        | n/a     |
| Ask `send-email -p attachments=~pgf/.config/pgf/master.key` | rejected  | n/a     |
| Drop files in `/var/lib/pgf-out` for the agent to consume   | ✅        | ✅      |
| Pull attachments via `download-attachment -p out=/var/lib/pgf-out/x.pdf` | ✅ | ✅ |

### Verification

After setup, confirm the whitelist is live with the attack pattern
documented in the project tests (`cmd/pgf/paths_test.go`):

```bash
# Should be rejected by pgf BEFORE any IMAP/SMTP traffic.
sudo -u agent -- env PGF_ALLOWED_PATHS=/var/lib/pgf-out \
    sudo -u pgf pgf run email/work send-email \
      -p to=test@example.com -p subject=test -p body=test \
      -p attachments=/home/pgf/.config/pgf/master.key
# expected: "error: action email/work.send-email: field \"attachments\" value \"/home/pgf/.config/pgf/master.key\" is outside PGF_ALLOWED_PATHS (/var/lib/pgf-out)"
```

## What pantograf does NOT (yet) protect against

- **Output exfiltration.** An agent can already `pgf run llm/x chat-completion`
  or `pgf run email/x send-email` with content of its choosing. Path
  enforcement does not (and cannot) review *what* the agent sends, only
  *where it reads files from / writes files to*. Limit the agent's
  capability to interesting connector instances at the sudoers / process
  level if this matters.
- **Disclosure via action output.** Some actions return content from the
  server (email bodies, voucher details). An agent that can read its own
  stdout sees these. Do not give the agent access to instances whose
  data is more sensitive than the agent itself.
- **Side-channel attacks on `master.key`.** A root-equivalent attacker
  on the box can read it. Use `LoadCredentialEncrypted=` or KMS-derived
  `PGF_MASTER_KEY` if your threat model requires root protection.
- **Triggers and `pgf serve`.** Webhook payloads received by `pgf serve`
  are not subject to `PGF_ALLOWED_PATHS` directly (no path inputs from
  the network are involved); the `response_file` credential setting is.

## Reporting Issues

Email <security@sistemica.de>. Please do not open public issues for
credential-handling or path-traversal bugs; coordinate disclosure
first.
