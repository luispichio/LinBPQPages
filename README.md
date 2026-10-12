# LinBPQPages

A web application for reading and composing messages on a [LinBPQ](https://www.cantab.net/users/john.wiseman/Documents/) instance. It is a sister project to [LinFBBPages](../LinFBBPages/), with direct access to the LinBPQ filesystem.

## Features

- Sign in with a callsign and password from `BBSUsers` in `linmail.cfg`.
- Browse a paginated, searchable message list from `DIRMES.SYS`, following LinBPQ privacy rules.
- Read messages from `Mail/m_%06d.mes`, including `R:` headers, RFC822/MIME content, and downloadable B2 attachments (with safe previews for raster images).
- Compose private messages (`SP`) or bulletins (`SB`) through `Mail/Import/mail.in`, with an indication of the configured import interval.
- Browse decoded 7+ files from `HTML/7plus/`.
- Responsive interface with English and Spanish translations.

## Architecture and data access

- Go backend using **only the standard library**, with a vanilla HTML/CSS/JS frontend embedded via `go:embed`.
- Reads `DIRMES.SYS`, `Mail/*.mes`, `linmail.cfg`, and `HTML/7plus/`.
- The only write is an append to `Mail/Import/mail.in`. The application does not modify messages, the index, configuration, or 7+ files.
- Sessions are kept in memory and use an `httpOnly`, `SameSite=Lax` cookie.
- The LinBPQ JSON API is not used: it currently exposes neither message bodies nor a sufficient message-submission endpoint.
- Detailed on-disk formats, message parsing, user-record fields, and queue/import behavior are documented in [`ARCHITECTURE.md`](ARCHITECTURE.md).

## Requirements

- Go 1.22+ to build.
- Read access to `DIRMES.SYS`, `Mail/`, `linmail.cfg`, and `HTML/7plus/`.
- Write access to `Mail/Import/` to create or append to `mail.in`.
- The LinBPQ HTTP service in this environment uses port `8080`; LinBPQPages defaults to `8081`.

## Build and run

```sh
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o linbpqpages ./cmd/linbpqpages
./linbpqpages --bpq-dir /home/luis/linbpq --listen :8081
```

Configure it with `--help` or environment variables:

| Flag | Environment variable | Default | Description |
|---|---|---|---|
| `--bpq-dir` | `LINBPQPAGES_BPQ_DIR` | `/home/luis/linbpq` | LinBPQ data directory. |
| `--listen` | `LINBPQPAGES_LISTEN` | `:8081` | HTTP listen address. |
| `--mail-cfg` | `LINBPQPAGES_MAIL_CFG` | `<bpq-dir>/linmail.cfg` | BBS configuration. Relative paths are resolved from `bpq-dir`. |
| `--sevenplus-dir` | `LINBPQPAGES_SEVENPLUS_DIR` | `<bpq-dir>/HTML/7plus` | Decoded 7+ files. |
| `--session-ttl` | `LINBPQPAGES_SESSION_TTL` | `8h` | Lifetime of in-memory sessions. |

Tests and lint:

```sh
go test ./...
go vet ./...
```

Fixtures are for tests and are read-only. To run the application, point `--bpq-dir` at a LinBPQ instance or a disposable working copy; do not use `design/fixtures` as the outbound message queue.

## HTTP API

The API uses JSON. All endpoints except `POST /api/login` require a session. Private responses use `Cache-Control: no-store`.

| Method | Path | Description |
|---|---|---|
| `POST` | `/api/login` | `{callsign,password}` → session cookie. |
| `POST` | `/api/logout` | Sign out. |
| `GET` | `/api/me` | Authenticated user. |
| `GET` | `/api/status` | BBS, import interval/status, queue size, and index counters. |
| `GET` | `/api/messages` | Filtered list; `type`, `q`, `page`, `page_size` (maximum 200). |
| `GET` | `/api/messages/{num}` | Metadata, routes, text, and attachment metadata. |
| `GET` | `/api/messages/{num}/attachments/{index}` | Download an attachment after checking message visibility. |
| `POST` | `/api/messages` | `{type:P\|B,to,via,title,body}` → enqueue a message. |
| `GET` | `/api/files` | List 7+ files. |
| `GET` | `/api/files/{name}` | Download a 7+ file; only regular basenames are accepted, and symlinks are rejected. |

## Security

- Callsigns are case-insensitive; passwords are case-sensitive. Users with an empty password or `F_Excluded` cannot sign in.
- Regular users can see bulletins/NTS messages except those with status `K` or `H`, and private messages only when they are the sender or recipient. Sysops can see all valid records. Hidden messages return `404`.
- `Mail/Import`, `Mail/Export`, `Mail/Restart`, and raw 7+ parts are not served.
- File names are validated and symlinks are not followed. Message content and attachment names are rendered as text; MIME HTML is never executed.
- `linmail.cfg` may contain plaintext passwords. Do not copy it into fixtures or this repository; `design/fixtures/linmail.cfg` uses synthetic credentials.
- The application is intended for trusted networks (LAN/VPN). If exposed outside the network, use TLS and perimeter authentication/a reverse proxy.

## Project structure

```text
ARCHITECTURE.md         # on-disk formats and outbound queue behavior
cmd/linbpqpages/        # binary and HTTP server
internal/bpq/           # libconfig, DIRMES, MIME, queue, and 7+ files
internal/api/           # HTTP API, privacy, and sessions
internal/config/        # flags and environment variables
web/static/             # embedded English/Spanish interface
design/fixtures/        # synthetic/sanitized examples for tests and development
```

## Roadmap

### Roadmap (current)

- Application information
  - References to the BPQ project
  - Version
  - License
  - Application repository
- BBS information
- Message list
  - Sort by columns
  - Infinite scrolling
- Messaging
  - File sending (7+ conversion)
  - CP437 Font (?)
- Read-message history and filtering by "unread"
- Login without password (read-only)
  - User request form
- Terminal
  - Web application <-> backend <-> Telnet <-> LinBPQ
- API
  - Documentation, examples

### Roadmap (future)

- LinBPQ instance management
  - Sysop
    - Configuration
    - User administration
    - Logs
    - ...

## License

This project is licensed under the [MIT License](LICENSE).

## Author

**Luis Pichio**

- [GitHub](https://github.com/luispichio)
- [QSL.net](https://qsl.net/lw6dio)
