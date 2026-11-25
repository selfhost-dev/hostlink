# SQL stream path (sql-stream-v1)

The console's SQL editor runs a statement on a database's machine and streams the rows to
the browser tab as they come, with progress and Cancel, through a relay. The backend
authorizes each run and records how it ended; it never sees a row. The protocol is the
selfhost backend's `docs/sql-stream-protocol.md`; this page is hostlink's half.

```
browser tab --wss--> relay (cmd/sqlrelay) <--wss-- agent's query service --http--> ClickHouse :8123
     ^                                                (app/services/sqlstream)        on loopback
     +-- ticket from the backend (Ed25519) ---------------- outcome to the backend --+
```

## Parts here

| Part | Where | What it does |
|---|---|---|
| Frame | `internal/sqlstream/wire` | version, kind, JSON header, body; one binary WebSocket message |
| Tokens | `internal/sqlstream/token` | the backend's signed tokens (compact JWS, EdDSA): agent, browser, ticket |
| Sealing | `internal/sqlstream/e2e` | P-256 key exchange signed with the agent's RSA key, HKDF-SHA256, AES-256-GCM with the frame's sequence number as nonce and its prefix as additional data |
| Relay | `internal/sqlstream/relay`, `cmd/sqlrelay` | routes frames by run; checks tokens and tickets; holds a run 20 s for its tab to come back; stores nothing |
| Query service | `app/services/sqlstream` | one outbound relay connection; runs tickets on ClickHouse's HTTP port with framing on; rows read exactly as the backend's job path reads them; caps, back-pressure, Cancel, resume; outcome to the backend |
| Developer tool | `cmd/sqlstream-dev` | test keys, a standalone agent, ticket minting, a timing probe |

## The query service

- Starts after registration unless `HOSTLINK_SQL_STREAM_ENABLED=false`; adds `sql-stream-v1` to
  `X-Agent-Capabilities`. The backend decides per agent: `GET /api/v1/agents/:id/sql_stream/session`
  answers a relay token, the relay's address and the signing keys, or 404 when the path is off
  (the agent asks again every 15 minutes).
- Checks every ticket: signature, type, audience, age, this agent, used once (2 minutes), the
  claims' shapes; opens the sealed part (the agent's RSA key, the run pid as additional data);
  makes the job path's checks (the account file's sync stamp, its runs logged, limits sized for
  this machine) and refuses with the same codes, which the backend mends on the job path.
- Runs as the read account with the settings its profile v2 lets it change in read-only mode:
  `framing_output_format`, `send_profile_events=0`, `http_wait_end_of_query=0`, `limit`
  (rows + 1), `max_execution_time`. ClickHouse 26.8 or later.
- Two workers (the account's `max_concurrent_queries_for_user`), a queue of 8, `busy` past that.
- Back-pressure: stops reading from ClickHouse while 4 MiB it sent is unacked.
- Cancel drops the request (ClickHouse cancels a read whose client goes, within about 0.3 s with
  framing's progress writes), then kills it by query id as the platform's login.
- Never logs a statement, a value or an engine message: run pid, query id, counts, timings,
  status and class only.

Overrides for a test harness: `HOSTLINK_SQL_STREAM_CLICKHOUSE_URL`, `HOSTLINK_SQL_STREAM_USERS_D`,
and the scripts' own `SELFHOST_TOTAL_RAM_MB` and `SELFHOST_CPU_CORES`.

## The relay

```
SQLRELAY_LISTEN=:8090
SQLRELAY_KEYS_URL=https://<api>/api/v1/sql_stream/keys      # or SQLRELAY_KEYS=kid:<b64u>,...
SQLRELAY_ALLOWED_ORIGINS=https://console.selfhost.dev,https://selfhost-uat.web.app
```

`/v1/agent` and `/v1/browser` (WebSocket), `/healthz` (agents, tabs and runs connected). TLS is
in front of it. It closes a connection with 4401 when its hello is refused, 4400 for a frame it
does not take, 4408 when it cannot keep up, 4409 when a newer connection of the same agent
replaces it, and 1012 when it restarts. Image: `dockers/sqlrelay.Dockerfile`.

## Running it on a laptop

```
go build -o /tmp/sqlrelay ./cmd/sqlrelay && go build -o /tmp/sqlstream-dev ./cmd/sqlstream-dev
/tmp/sqlstream-dev keys -dir /tmp/devkeys                     # prints SQLRELAY_KEYS
SQLRELAY_KEYS=... SQLRELAY_ALLOWED_ORIGINS=http://localhost:54588 SQLRELAY_LISTEN=127.0.0.1:8090 /tmp/sqlrelay
/tmp/sqlstream-dev agent -dir /tmp/devkeys -relay ws://127.0.0.1:8090 \
  -clickhouse http://127.0.0.1:8123 -users-d <a users.d whose z_selfhost_sql_editor.xml names the stamp>
/tmp/sqlstream-dev probe -dir /tmp/devkeys -password-file <file> -db default -sql "select 1" -runs 50 -quiet
```

`mint` makes a browser token and a ticket for a tab's key, for the console's `/dev/sql-stream`
page. Test keys only: the tool never sees the backend's.

## Checked (2026-11-25)

On ClickHouse 26.8.45.6, locally and on a QA machine with 150 million rows: values identical to
the backend's job-path parser on every edge case tried (escapes, NULL against the text `\N`,
arrays and maps as written, 64-bit and 128-bit extremes, invalid UTF-8); 10,000 rows of nine
columns in 1.3 s; progress every 0.5 s over a 150-million-row scan; Cancel stopping the query
on the engine about 0.3 s after it was sent; a 48-second query finishing where the job path
stops at 30 s.
