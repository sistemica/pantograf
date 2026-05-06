# transport/smtp

Thin wrapper around [`wneessen/go-mail`](https://github.com/wneessen/go-mail)
for outbound SMTP. The connector builds a `*mail.Msg` and hands it here.

## Surface

| Function | Purpose |
|---|---|
| `Send(ctx, Config, ...*mail.Msg) error` | Dial, AUTH, transmit, tear down. One short-lived connection per call. |
| `Probe(ctx, Config) error` | Dial + AUTH + close. No message sent. Used by credential wizards to catch bad creds at setup. |

## Config

| Field | Type | Default | Notes |
|---|---|---|---|
| `Host` | string | — | required |
| `Port` | int | 465 (TLS) / 587 (STARTTLS) / 25 (none) | |
| `Security` | `Security` | `SecurityTLS` | TLS / STARTTLS / None |
| `Username` | string | — | |
| `Password` | string | — | |
| `InsecureSkipVerify` | bool | false | |

## Security modes

| Const | Wire | Typical port |
|---|---|---|
| `SecurityTLS` | implicit TLS on connect | 465 |
| `SecuritySTARTTLS` | EHLO + STARTTLS upgrade | 587 |
| `SecurityNone` | plaintext (test only) | 25 |

## Example

```go
import (
    smtp "github.com/sistemica/mw/transport/smtp"
    "github.com/wneessen/go-mail"
)

msg := mail.NewMsg()
_ = msg.From("me@example.com")
_ = msg.To("you@example.com")
msg.Subject("hi")
msg.SetBodyString(mail.TypeTextPlain, "hello")

err := smtp.Send(ctx, smtp.Config{
    Host:     "smtp.fastmail.com",
    Port:     465,
    Security: smtp.SecurityTLS,
    Username: "me@example.com",
    Password: "app-pass",
}, msg)
```

## What this package does NOT do

- Message construction — use `go-mail` directly (`mail.NewMsg`, `AttachFile`, ...).
- Long-lived connection pooling — each call is its own short-lived dial.
- DKIM signing / DSN — handled by `go-mail` if configured at message level.

## Errors

- Probe returns `smtp: probe: <reason>` for dial / AUTH failures.
- Send returns `smtp: send: <reason>` for any failure during transmit.
- Both wrap the underlying `go-mail` error so the original cause is reachable
  via `errors.Is` / `errors.As`.
