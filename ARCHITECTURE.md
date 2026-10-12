# Architecture

This document describes LinBPQPages' on-disk data formats and outbound message queue. Implementation requirements and security rules are in [`AGENTS.md`](AGENTS.md).

## `DIRMES.SYS` binary format

The source of truth is `struct MsgInfo` in `linbpq.source/bpqmail.h` (currently around lines 615–658). The type is declared under `#pragma pack(1)`, inherited from `OldMsgInfo`, so there is **no padding**. The record size verified on the installation is **308 bytes**, in little-endian byte order.

| Field | Offset | Size |
|---|---:|---:|
| `type` | 0 | 1 |
| `status` | 1 | 1 |
| `number` | 2 | 4 (int32) |
| `length` | 6 | 4 (int32) |
| `xdatereceived` | 10 | 4 |
| `bbsfrom[7]` | 14 | 7 |
| `via[41]` | 21 | 41 |
| `from[7]` | 62 | 7 |
| `to[7]` | 69 | 7 |
| `bid[13]` | 76 | 13 |
| `title[61]` | 89 | 61 |
| `nntpnum` | 150 | 4 |
| `B2Flags` | 154 | 1 |
| `xdatecreated` | 155 | 4 |
| `xdatechanged` | 159 | 4 |
| `fbbs[20]` | 163 | 20 |
| `forw[20]` | 183 | 20 |
| `emailfrom[41]` | 203 | 41 |
| `Locked` | 244 | 1 |
| `Defered` | 245 | 1 |
| `UTF8` | 246 | 1 |
| `datereceived` | 247 | 8 (int64 epoch) |
| `datecreated` | 255 | 8 (int64 epoch) |
| `datechanged` | 263 | 8 (int64 epoch) |
| `Spare[37]` | 271 | 37 |
| **Total** | | **308** |

`internal/bpq/layout.go` is the only definition of these offsets; parsers must not hardcode them. Record 0 is the control record: `status=2`, `number` is the number of slots (`N`), and `length` is the last assigned message number. The file size must be `(N+1)*308`. Slots with `type==0` or `status==0` are not messages. The API sorts messages by descending number. Ignore the legacy `x*` fields; current dates are int64 values at the offsets shown above. If `UTF8==0`, index text fields use Windows-1252; if `UTF8!=0`, preserve valid UTF-8.

LinBPQ replaces the entire `DIRMES.SYS` file (`SaveMessageDatabase`, `BBSUtilities.c`), and Housekeeping may renumber `.mes` files. Reload when size or modification time changes, verify that the file did not change while being read, and serve the last valid cache if a replacement is incomplete. Do not keep a durable mapping between a message number and a body file.

## Message bodies in `Mail/`

- Body files use the flat path `Mail/m_%06d.mes` (no digit-based subdirectories).
- Open a body only if its message number exists in the current index and the user is allowed to view it.
- Parse initial `R:` headers and the blank separator line. The remaining content may be plain text, RFC822, or MIME multipart/B2. Use the standard library for MIME charset and transfer-encoding handling.
- Attachments are embedded in the `.mes` file; never extract them to disk. Expose content only through an endpoint that rechecks the session and message visibility. Serve only safe raster formats inline; serve HTML/SVG as attachments and never execute them.
- `K`/Housekeeping can leave an index entry without a `.mes` file. Return metadata with `body_available=false` rather than treating this as a fatal error.
- Limit `.mes` reads to 16 MiB; bound MIME parts and nesting depth.

## Users in `linmail.cfg`

The application-specific parser supports the data syntax needed here: groups, assignments, scalars, arrays, escaped strings, and `#`, `//`, and `/* */` comments. In the real configuration, `BBSName` and `SYSOPCall` are in the `main` group; `BBSUsers` and `BBSForwarding` may be at the root. Also accept `BBSUsers` inside `main`.

Each `BBSUsers` value has caret-delimited fields, as read by `GetUserDatabase` in `BBSUtilities.c`:

```text
Name^Address^HomeBBS^QRA^pass^ZIP^CMSPass^lastmsg^flags^PageLen^BBSNumber^RMSSSIDBits^WebSeqNo^TimeLastConnected^StatsTotal^StatsLast
```

- `flags` is decimal and is field index 8; `F_Excluded=0x0001` and `F_SYSOP=0x0008` (`bpqmail.h`).
- Callsigns are case-insensitive; passwords are case-sensitive. Reject empty passwords and excluded users at login.
- Keep passwords in memory only; never serialize them.
- The legacy binary `BPQBBSUsers.dat` format is not supported in Stage 1. Fail with a clear error if `BBSUsers` is missing.

## Outbound message queue

The import record format is `SP DEST [@ ROUTE] < SOURCE [$BID]`, followed by the subject on the next line, the message body, and `/EX`. Use `SB` instead of `SP` for bulletins. `DEST` and `SOURCE` callsigns are up to 6 characters; `ROUTE` is up to 40 characters. LinBPQ truncates the subject to 60 bytes, so validate these limits before writing. Reject control characters, NULs, multiline fields, recipient delimiters, and any body line equal to `/EX`.

Write one complete message at a time to `Mail/Import/mail.in`, using `O_APPEND` and `flock` on the file itself (do not create lockfiles). LinBPQ should import the queue through an existing `BBSForwarding` entry, for example:

```text
ConnectScript = "IMPORT ./Mail/Import/mail.in DELETE";
FwdInterval = 600;
```

Do not add a second group for a callsign that is already configured; add these settings to its existing group. The application reads this configuration and reports `FwdInterval` as an approximate maximum delay. If the import script is missing, it warns but still allows messages to be queued.

The advisory lock coordinates only writers that take the same lock. LinBPQ does not take it when importing and deleting the queue, so a small race window remains if a message is queued while `IMPORT ... DELETE` runs. The queue provides no delivery acknowledgement; do not promise confirmed delivery. For critical messages, verify that the queue was imported.
