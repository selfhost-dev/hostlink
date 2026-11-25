# The SQL stream relay lives in hostlink

The SQL workbench's fast path (sql-stream-v1) has three new parts: the backend signs a
ticket for each run, a relay passes frames between the browser tab and the agent, and the
agent's query service runs the statement and streams the rows. The relay is
`cmd/sqlrelay` here, not a repository of its own, and it shares `internal/sqlstream`
(the frame, the tokens, the end-to-end sealing) with the agent.

The relay and the agent are the two Go ends of one protocol. Kept together, a change to
the frame, a token's claims or the sealing is one commit with one set of tests and one set
of test vectors (`internal/sqlstream/testdata/sqlstream_vectors.json`, which the backend's
and the console's tests open too), and the relay can never speak a version the agent was
not built against.

## Considered Options

- **A repository of its own** (the plan's first option): rejected for now, because every
  protocol change would be two pull requests that must land together, and the protocol
  package would be duplicated or published as a module.
- **Inside the Rails backend** (ActionCable): rejected. The backend's agent connections are
  per process, its cable adapter writes every message to a database table and keeps it a
  day, which result rows must never do, and its web tier has few threads for long streams.
- **Cloudflare Workers with Durable Objects:** kept for later. With end-to-end sealing the
  relay sees only ciphertext, so a third party could run it; it is not needed to start.

## Consequences

- The relay is released and deployed from this repository's tags, as its own image
  (`dockers/sqlrelay.Dockerfile`), next to the backend; it does not ship in the agent's
  binary.
- A protocol change bumps the frame version or adds a capability (`sql-stream-v2`), and the
  backend sends tickets only to agents that report the capability.
- The relay stores nothing, so a restart only makes agents and tabs reconnect; running two
  needs a shared lookup of which relay holds which agent, a later step.
