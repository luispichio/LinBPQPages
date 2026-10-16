# AGENTS.md — LinBPQPages

Project change guide. Read this file in full before modifying parsers, authorization, or filesystem access.

## Purpose and design decisions

A sister web application to LinFBBPages for a LinBPQ instance. Stage 1 includes login, message list and detail views, composition through `Mail/Import/mail.in`, and a gallery for `HTML/7plus/`.

- Data is read directly from the filesystem; the LinBPQ HTTP API does not provide message bodies or submission.
- Go standard-library-only; vanilla frontend with no build/npm step, embedded with `go:embed`; English/Spanish UI.
- The only write beneath the BBS directory is an append to `Mail/Import/mail.in`.
- Do not copy configuration, passwords, or real messages into `design/fixtures/`. Project fixtures must be synthetic.

## Project structure and commands

```text
cmd/linbpqpages/       # entry point
internal/bpq/          # LinBPQ layout, parsers, and Store
internal/api/          # handlers, sessions, and visibility rules
internal/config/       # flags + environment variables
web/static/            # embedded HTML/CSS/JS and i18n
design/fixtures/       # sanitized/synthetic examples, read-only for tests
```

```sh
CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o linbpqpages ./cmd/linbpqpages
go test ./...
go vet ./...
```

For a manual run, use a LinBPQ instance or a disposable copy; do not point the outbound message queue at `design/fixtures`.

## Implementation constraints

- **Do not add external dependencies** without explicit approval. Target modest Linux systems (≈1 GHz / 512 MB RAM) and a `CGO_ENABLED=0` binary.
- Use `net/http` for the backend; do not generate the frontend or add an npm/build step.
- Sessions are in memory only, use 32 random bytes from `crypto/rand`, and use an `httpOnly`, `SameSite=Lax` cookie.
- Documentation is in English, code comments are in Spanish, and the UI is translated into English and Spanish.
- Every new parser requires tests. Update `README.md`, `ARCHITECTURE.md`, and this file in the same change when formats, the API, security, or commands change.

## On-disk formats and parsing

See [`ARCHITECTURE.md`](ARCHITECTURE.md) for detailed record layouts, `linmail.cfg` fields, and the outbound queue format and setup.

### `DIRMES.SYS`

- `internal/bpq/layout.go` is the sole definition of offsets. **Never hardcode offsets in parsers.**
- Record 0 is the control record; validate its slot count and the expected `(N+1)*308` file size. Records with `type==0` or `status==0` are never messages; sort API results by descending number.
- Ignore legacy `x*` date fields; the current date fields are int64. Decode index text by content, not by the `UTF8` flag: valid UTF-8 is preserved, and otherwise LinBPQ's `TrytoGuessCode` heuristic chooses CP437, CP1251, or Windows-1252 (see `ARCHITECTURE.md`). LinBPQ does not set that flag when importing messages and ignores it when rendering its own webmail.
- LinBPQ replaces the whole index and Housekeeping may renumber `.mes` files. Reload when size/mtime changes, ensure the file remains stable during a read, and keep serving the last valid cache if a replacement is incomplete. Never cache a durable message-number-to-file mapping.

### Message bodies in `Mail/`

- A body may be opened only if its number exists in the current index and the user is allowed to view it.
- Parse initial `R:` headers and the blank line; the rest may be text, RFC822, or MIME multipart/B2. Use the standard library for MIME charset and transfer-encoding handling. A declared MIME charset always wins; without one (or with an unknown one), decode by content with the same rule as the index.
- Attachments are embedded in `.mes` files; never extract them. Serve content only from an endpoint that rechecks the session and visibility. Serve only safe raster formats inline; HTML/SVG must be attachments and must never execute.
- `K`/Housekeeping may leave an index entry without a `.mes` file: return metadata with `body_available=false`, not a fatal error.
- Limit `.mes` reads to 16 MiB; bound MIME parts and nesting depth.

### Users in `linmail.cfg`

- The parser must support the required data syntax: groups, assignments, scalars, arrays, escaped strings, and `#`, `//`, and `/* */` comments. `BBSName` and `SYSOPCall` are in `main`; `BBSUsers` and `BBSForwarding` may be at the root. Also accept `BBSUsers` inside `main`.
- `flags` is decimal, field index 8; `F_Excluded=0x0001` and `F_SYSOP=0x0008` (`bpqmail.h`). Callsigns are case-insensitive; passwords are case-sensitive. Reject empty passwords and excluded users at login.
- Keep passwords in memory only and never serialize them. The legacy binary `BPQBBSUsers.dat` format is not supported in Stage 1. If `BBSUsers` is missing, return a clear error.

### Outbound message queue

- Follow the format and LinBPQ import setup in [`ARCHITECTURE.md`](ARCHITECTURE.md). Validate field limits and reject controls, NULs, multiline fields, recipient delimiters, and body lines equal to `/EX` before writing.
- Append one complete message at a time to `Mail/Import/mail.in` with `O_APPEND` and `flock` on the file itself; do not create lockfiles.
- The advisory lock does not synchronize with LinBPQ. Document the import race and never promise confirmed delivery; the queue provides no acknowledgement.

## Visibility and API

- Records with `type==0` or `status==0` are never shown.
- Sysops may access every valid record, including `K` and `H` (a deliberate policy matching the sysop view in LinFBBPages).
- Non-sysops may see `B` and `T` records except those with status `K` or `H`. They may see `P` records except those with status `K` or `H` only when `from` or `to` exactly matches their callsign (case-insensitive). LinBPQ does not strip SSIDs in `CheckUserMsg`; do not strip them here.
- Unknown types are hidden from regular users; sysops may see them.
- Inaccessible message details and attachments return `404` to avoid confirming that they exist.
- All API endpoints except login require authentication. Never trust a frontend-supplied ID or filename without validating it and rechecking visibility.

API: `POST /api/login`, `POST /api/logout`, `GET /api/me`, `GET /api/status`, `GET/POST /api/messages`, `GET /api/messages/{num}`, `GET /api/messages/{num}/attachments/{index}`, `GET /api/files`, `GET /api/files/{name}`.

## Gallery and filesystem

- List only regular files from `HTML/7plus/`; exclude `list.csv`, `index.html`, symlinks, and directories.
- Do not list `Mail/Import`, `Mail/Export`, `Mail/Restart`, `7plus/extracted`, or `.pNN` parts.
- Validate each name as a basename with no `/`, `\\`, `..`, or NUL; reject symlinks. Determine MIME type from content.
- **Never write to** `DIRMES.SYS`, `Mail/*.mes`, `linmail.cfg`, `BPQBBSUsers.dat`, or `HTML/7plus/**`.
- `design/` fixtures are read-only and synthetic/sanitized. Tests must use `t.TempDir()` for any writes.

## Required tests

Run `go test ./...` and `go vet ./...`. Maintain coverage for DIRMES offsets, invalid headers/slots, CP1252/CP437/CP1251/UTF-8 detection, authentication and flags, privacy, MIME base64/quoted-printable/attachments, queue validation, concurrent appends, symlinks, and path traversal. Tests must not depend on `/home/luis/linbpq` or real passwords/data.
