# AGENTS.md

Rules for agents working in this repository.

## Design

- The server is one package, `zdxsv/` (`package main`): the lobby
  server is `lbs` (`lbs_*.go`), the battle server `mcs` (`mcs_*.go`).
  Generated protobuf code stays in `zdxsv/proto/`; zproxy is its own
  module in `src/zproxy` and imports no server code.
- `lbs_msg_*.go`: decode the message, call a `Lbs` method, encode the
  answer. A fixed answer needs no `Lbs` call. Decisions and state
  changes live in `Lbs`, and `Lbs` stays unit-testable. No package
  boundary enforces this; keep it by hand.
- Do not scatter one component over many small files.

## Tests

Three kinds:

- Scenario tests: show that a real client works, by talking to the
  server the way the client does. They run without a TCP server.
- Unit tests of `Lbs`: what scenario tests cannot cover, such as
  boundary values and error handling.
- Smoke test (`scripts/smoke.sh`): the server starts and answers. It
  also covers the TCP servers that scenario tests leave out.

No fixed sleeps.

## Comments

- None by default. Test: delete it; if the code is still clear at a
  glance, it was not needed. Write one only for information the code
  cannot show (a non-obvious why, a constraint, a trap). No history.
- No references to or comparisons with other implementations (gdxsv,
  flycast, pcsx2): they go stale when the other side changes. Parity
  notes go in the PR body or docs.
- Do not explain other code in the project; name it if needed.
- Self-contained: no session ids, issue numbers, private scripts or
  repositories.
- Update comments with the code. English.
