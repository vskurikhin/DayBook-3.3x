# :rowboat:  Raft

[Русская версия](README.md)

An implementation of the Raft distributed consensus algorithm in Go,
with a replicated key–value service exposing an HTTP API built on top
of it.

## About the project

The repository contains the working code of the Raft consensus module
(leader election, log replication, commit, snapshots) and of a
replicated key–value store with an HTTP API, a Go client, and a load
generator. The working code is the root package of the module
`github.com/vskurikhin/raft`, `pkg/...`, `cmd/...`, `internal/...`.

The project is accompanied by a series of articles:

* [Part 0: Introduction](https://svn.su/2020/2020-02-22-implementing-raft-part-0-introduction.html)
* [Part 1: Elections](https://svn.su/2020/2020-02-24-implementing-raft-part-1-elections.html)
* [Part 2: Commands and log replication](https://svn.su/2020/2020-02-29-implementing-raft-part-2-commands-and-log-replication.html)
* [Part 3: Persistence and optimizations](https://svn.su/2020/2020-05-05-implementing-raft-part-3-persistence-and-optimizations.html)
* [Part 4. Key–value database](https://svn.su/2024/2024-10-10-implementing-raft-part-4-key-value-database.html)
* ~~Part 5. Exactly-once message delivery (Exactly-Once Delivery)~~ _todo_

The directories `part1`–`part5kv` are educational stages of building
the project, matching the parts of the article series (part 0 is an
introduction and contains no code). Each educational directory is
self-contained and repeats the code of the previous parts: comparing
the directories shows how the implementation evolved. The working code
is developed separately from the educational directories; long test
runs over the educational directories are not performed (see the
“Testing” section).

## Features

* leader election with pre-voting (pre-vote) that does not increase
  the term when connectivity with the cluster is lost;
* log replication and commit of entries by a quorum;
* state machine snapshots and node restore from a snapshot;
* cluster membership changes — adding a voting or non-voting node,
  demoting a voter, removing a server (one server per change);
* leadership transfer to a specified node without a new election;
* futures for asynchronous client operations;
* TCP and in-process transports; the transport is created by the
  caller and passed to the node through the configuration;
* a replicated KV service with a REST API over HTTP;
* a Go client for the KV service with leader discovery and request
  retries;
* the `loadkv` load generator.

## Project layout

```
.
├── raft.go                  — ConsensusModule: configuration, timers, timing bounds
├── raft_cm.go               — ConsensusModule: core, life cycle, shutdown
├── raft_cm_election.go      — leader election and pre-voting
├── raft_cm_leader.go        — leader loop, client operations, membership changes
├── raft_cm_replication.go   — log replication, heartbeat, check quorum
├── raft_cm_rpc.go           — RPC handling: AppendEntries, RequestVote, RequestPreVote
├── raft_cm_log.go           — the log of entries
├── raft_cm_apply.go         — applying committed entries to the state machine
├── raft_cm_snapshot.go      — creating and installing snapshots
├── raft_cm_storage.go       — persisting durable state and restore on start
├── raft_cm_metrics.go       — per-second statistics: latency and counters
├── aliases.go               — re-export of domain types from pkg/raft/contract
├── commitment.go            — tracking entry commit by a quorum
├── configuration.go         — cluster configuration and its changes
├── future.go                — futures for asynchronous operations
├── server.go                — Server: binds the transport and the ConsensusModule
├── trace.go                 — trace thresholds of the consensus module
├── cmd/                     — executables: raftkv (node), loadkv (load generator)
├── internal/config/         — command-line flag parsing for raftkv
├── internal/load/config/    — command-line flag parsing for loadkv
├── internal/_init/          — start-up initialization: flags and tracing
├── pkg/api/                 — REST API request and response types
├── pkg/kvclient/            — Go client for the KV service
├── pkg/kvservice/           — KV service: HTTP API, DataStore, the Raft state machine
├── pkg/raft/contract/       — domain types, interfaces, and RPC structures
├── pkg/raft/store/          — log and snapshot stores
└── pkg/raft/transp/         — TCP and in-process transports
```

## Architecture

The layers, top to bottom:

```
┌─────────────────────────────────────────────────────────────────┐
│ KVService (pkg/kvservice)                                       │
│ HTTP API • DataStore • implements raft.FSM                      │
└─────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│ Server (server.go)                                              │
│ binds the externally provided transport and ConsensusModule     │
└─────────────────────────────────────────────────────────────────┘
                               │
                               ▼
┌─────────────────────────────────────────────────────────────────┐
│ ConsensusModule (raft_cm_*.go)                                  │
│ depends only on the interfaces Transport, Storage,              │
│ SnapshotStore, FSM                                              │
└─────────────────────────────────────────────────────────────────┘
```

* **KVService** accepts HTTP requests, enqueues write commands to the
  consensus module, and applies committed log entries to the local
  DataStore, implementing the `raft.FSM` interface.
* **Server** is a wrapper that creates a ConsensusModule on top of the
  transport passed via `Config.Transport`; it connects and disconnects
  peers and stops the node.
* **ConsensusModule** implements the Raft protocol and depends only on
  the interfaces `Transport`, `Storage`, `SnapshotStore`, `FSM` — the
  store and the transport are passed to the node from the outside
  through the configuration.

The state machine of the consensus module:
```
Start ────────────┐
                  ▼                      higher term
               FOLLOWER ◄───────────────────┐detected
               │     ▲      (becomeFollower)│
               │     │                      │
       election│     │live                  │
        timeout│     │leader              LEADER
startElection()│     │detected              ▲
               ▼     │                      │
        PRE-CANDIDATE│                      │
               │     │                      │
       pre-vote│     │                      │
        success│     │                      │
               │     │                quorum│
               CANDIDATE ───────────────────┘
               │     ▲               of votes
               └─────┘
any state ──(Stop)──► DEAD
```

A node starts as a follower. When its election timer expires, it first
runs a pre-vote without increasing the term — this prevents disruptive
re-elections by a node that temporarily lost connectivity. On pre-vote
success the node becomes a candidate and runs the election; with a
quorum of votes it becomes the leader. A higher term or detection of a
live leader returns the node to follower from any active state.
Stopping the node (`Stop`) moves it to the DEAD state.

## Quick start

```sh
make build          # builds bin/raftkv and bin/loadkv
make start-raft     # stand: 3 nodes + a load generator
make stop-raft      # stops the stand
```

The `make start-raft` target first stops a previous stand, then starts
3 raftkv nodes: the HTTP API listens on ports 8881–8883, RPC on ports
9991–9993. Two seconds after the nodes start, the loadkv load
generator is started; by default it runs for 5 minutes
(`DURATION=5m`) and exits by itself.

Stopping and cleanup: `make stop-raft` stops the generator and the
nodes; `make stop` also removes pid files; the targets `make clean`,
`make clean-build-files`, `make clean-data-files`, `make clean-pid-files`,
`make clean-trace-files` remove build, data, and trace artifacts.

Stand variables can be overridden on the make command line without
editing the Makefile, for example `make start-raft DURATION=1m`:

| Variable | Node or generator flag | Makefile default |
|---|---|---|
| `APPLY_BATCH_INTERVAL` | `-apply-batch-interval` | 50 |
| `HEARTBEAT_TIMEOUT` | `-heartbeat-timeout` | 45 |
| `REELECTION_TIMEOUT` | `-reelection-timeout` | 500 |
| `TICKER_TIMEOUT` | `-ticker-timeout` | 20 |
| `TRACE_LOG_LEVEL` | `-trace-log-level` | 0 |
| `CONCURRENCY` | `-concurrency` | 8 |
| `DELETE_PERCENT` | `-delete-percent` | 0 |
| `DURATION` | `-duration` | 5m |
| `GET_PERCENT` | `-get-percent` | 75 |
| `REQUEST_RATE` | `-request-rate` | 200 |
| `VALUE_SIZE` | `-value-size` | 128 |
| `VERIFY_PERCENT` | `-verify-percent` | 33 |
| `WEAK_GET_PERCENT` | `-weak-get-percent` | 0 |
| `STAND` | — | empty |

The `STAND` variable sets a prefix for stand artifact file names
(traces, stdout/stderr, pid files) and lets artifact sets be told
apart. The prefix does not change node ports or data directories, so
two stands cannot run at the same time in one working tree.

## HTTP API

The KV service accepts JSON requests over six routes:

| Route | Purpose |
|---|---|
| `GET /verifyleader/` | check that the node is the acting leader |
| `GET /weak-get/{key...}` | weak read of a key value without a log write |
| `POST /cas/` | conditional write: compare-and-swap of values |
| `POST /delete/` | delete a key |
| `POST /get/` | read a key value through the log |
| `POST /put/` | write a key value |

Business responses always come back with HTTP 200; the outcome is
carried by the `RespStatus` field: `StatusOK` — the operation
succeeded; `StatusNotLeader` — the operation was not confirmed by this
node (the node is not the leader, the timeout for enqueueing the
command into the consensus module queue has expired, leadership is
lost or being transferred, the node is shutting down), and the client
should retry the command at another address. The full reference of
routes, request and response formats, delivery guarantees, and the Go
client behavior is [docs/API-HTTP.md](docs/API-HTTP.md).

## Configuration and flags

Default values of the node timing parameters:

| Parameter | Node (flag default) | Stand (Makefile) |
|---|---|---|
| Heartbeat period (`-heartbeat-timeout`) | 33 ms | 45 ms |
| Election timeout base (`-reelection-timeout`) | 340 ms | 500 ms |
| Election ticker tick (`-ticker-timeout`) | 20 ms | 20 ms |
| Apply batch interval (`-apply-batch-interval`) | 50 ms | 50 ms |
| Trace level (`-trace-log-level`) | 1 | 0 |

The parameters are tied by the invariant
`reelection-timeout ≥ 10·heartbeat-timeout` (as well as
`ticker-timeout ≤ reelection-timeout/10` and
`apply-batch-interval ≤ reelection-timeout`). The check runs at startup
with a fail-fast strategy: an invalid combination terminates the
process before any work starts, and the message lists every violation.

The full reference of raftkv and loadkv flags with defaults and value
bounds is [docs/Flags.md](docs/Flags.md); the RPC transport timeout
and the remaining flags are described only there.

## Testing

```sh
go test . ./pkg/...      # working packages: the root package and pkg/...
go test -race . ./pkg/...
make test-stress COUNT=3 # sequential runs under -race
```

Root package tests use real timers and are slow to run — this is
expected; the run timeout is set with a margin. The `partN`
directories are educational: long test runs and stress tests are not
run over them; the stress target and repeats are run over the working
packages. The tests use leaktest to detect goroutine leaks — the only
external dependency of the project, used in tests only.

## Observability

The stand writes consensus module and KV service traces to the files
`trace/raftcm-N.trace` and `trace/raftkv-N.trace`, and the nodes’
standard outputs to `trace/raftkv-N.stdout` and
`trace/raftkv-N.stderr`. Profiling flags: `-pprof-addr` (address of a
dedicated profiling HTTP server; empty disables it),
`-block-profile-rate`, `-mutex-profile-fraction`.

The `-trace-log-level` flag sets the trace threshold of the consensus
module. A message of level l is printed when l < threshold; the message
layer constants are 0/1/2/4/8/16. The flag works as follows:

* with `-trace-log-level=0`, the consensus module’s per-second
  statistics output to stdout is fully disabled: neither the latency
  line nor the counters line is printed;
* with a value ≥ 1, both lines are printed once per second — the
  latency report and the counters report;
* higher threshold steps add layers of trace messages without turning
  off the previous ones: 1 — key events (role and term changes,
  voting), 2 — the flow of the election and leader loops, 3 —
  progress events (election win, commit index advance), 5 — pre-vote
  and snapshot delivery, 9 — replication (AppendEntries, heartbeat),
  17 — full log dump;
* the default value of the flag (in the compiled binary) is 1; the
  Makefile sets `TRACE_LOG_LEVEL=0` for the stand, so the per-second
  statistics output to stdout is disabled.

Trace files are set by the flags `-trace-cm-log-file` and
`-trace-kv-log-file` (empty value — the standard error stream).

## Documentation

* [docs/README.md](docs/README.md) — documentation index;
* [docs/API-ConsensusModule.md](docs/API-ConsensusModule.md) —
  consensus module API reference (package `raft`); an English twin
  `docs/API-ConsensusModule-en.md` exists;
* [docs/Flags.md](docs/Flags.md) — command-line flag reference for
  raftkv and loadkv; an English twin `docs/Flags-en.md` exists;
* [docs/API-HTTP.md](docs/API-HTTP.md) — HTTP API reference of the KV
  service; an English twin `docs/API-HTTP-en.md` exists.

## License

The code is distributed under the license whose text is in the
[LICENSE](LICENSE) file.

## Version synchronization

The Russian version (`README.md`) is the primary one. The English
`README-en.md` is a translation.
