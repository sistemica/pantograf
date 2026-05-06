# transport/imap

Thin wrapper around [`emersion/go-imap/v2`](https://github.com/emersion/go-imap)
that handles dial + auth from a flat `Config`. Connector code uses the returned
`*imapclient.Client` directly for fetch / search / append / IDLE.

## Surface

| Function | Purpose |
|---|---|
| `Dial(Config) (*imapclient.Client, error)` | Open connection + LOGIN. Caller owns Logout/Close. |

## Config

| Field | Type | Default | Notes |
|---|---|---|---|
| `Host` | string | — | required |
| `Port` | int | 993 (TLS) / 143 (other) | |
| `Security` | `Security` | `SecurityTLS` | TLS / STARTTLS / None |
| `Username` | string | — | full email address typically |
| `Password` | string | — | app-password preferred |
| `InsecureSkipVerify` | bool | false | opt-in only |

## Security modes

| Const | Wire | Typical port |
|---|---|---|
| `SecurityTLS` | implicit TLS on connect | 993 |
| `SecuritySTARTTLS` | upgrade after greeting | 143 |
| `SecurityNone` | plaintext (test only) | 143 |

## Example

```go
import imap "github.com/sistemica/mw/transport/imap"

cli, err := imap.Dial(imap.Config{
    Host:     "imap.fastmail.com",
    Port:     993,
    Security: imap.SecurityTLS,
    Username: "user@example.com",
    Password: "app-password",
})
if err != nil { return err }
defer func() {
    _ = cli.Logout().Wait()
    _ = cli.Close()
}()

// use cli — Select, Fetch, Search, Append, IDLE, ...
```

## What this package does NOT do

- Fetch / search / append helpers — those belong in the connector that uses IMAP.
- Multipart body parsing — connector concern.
- IDLE — call it directly on the returned client.
- Connection pooling — one open client per session.

## Errors

`Dial` returns wrapped errors: `imap: dial <addr>: ...` for TCP/TLS failures,
`imap: login <user>: ...` for auth failures. Both close the underlying socket
before returning.
