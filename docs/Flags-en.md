# Command-Line Flags

## Purpose of This Document

This reference covers the command-line flags of the two programs in the
repository: the `raftkv` node and the `loadkv` load generator. For every
flag it lists the name, the default, the unit, and the bounds of
acceptable values. Unacceptable values are detected at startup by the
fail-fast strategy: the process exits before doing any work, and the
message names the flag, the actual value, and the requirement.

This document covers command-line flags only. Program interfaces (the
HTTP interface of the KV service, the API of the `raft` package) and
environment variables are described in separate documents.

### Timing Parameters

Validated at startup by `raft.ValidateTiming` (see the section "Timing
Invariants"); values are expressed as a whole number of milliseconds.

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-apply-batch-interval` | 50 ms | duration | [1 ms; 5000 ms], whole ms, ≤ reelection-timeout |
| `-heartbeat-timeout` | 33 ms | duration | [5 ms; 82 ms], whole ms; 82 = ⌊330/4⌋ |
| `-reelection-timeout` | 340 ms | duration | [200 ms; 30 000 ms], whole ms, ≥ 10·heartbeat-timeout |
| `-ticker-timeout` | 20 ms | duration | [1 ms; 1000 ms], whole ms, ≤ reelection-timeout/10 |

- `-apply-batch-interval` — the interval at which the leader loop
  checks whether committed entries must be applied to the state
  machine; commit-channel notifications accumulated over the interval
  are merged into a single batch.
- `-heartbeat-timeout` — the leader heartbeat period. The upper bound
  of 82 ms is derived from the fixed 330 ms check-quorum timeout:
  within the checking window the leader must contact its peers at
  least four times; rounding down to whole milliseconds gives
  ⌊330/4⌋ = 82 ms (`raft.go`, `MaxHeartbeatTimeout`).
- `-reelection-timeout` — the base of the election timeout: the actual
  timeout is drawn at random from [reelection-timeout;
  2·reelection-timeout); the same window bounds the pre-vote
  suppression on the receiving side.
- `-ticker-timeout` — the election timer polling tick; it determines
  the quantization error of the election timeout.

### RPC

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-tcp-rpc-timeout` | 165 ms (raft.TCPRPCTimeout) | duration | > 0 |

`-tcp-rpc-timeout` — the timeout of a single RPC call to a peer in the
TCP transport. The leader check-quorum timeout is fixed at 330 ms
(2 × 165 ms) and does NOT scale with this flag.

### Snapshots

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-snapshot-interval` | 3 s | duration | > 0 |
| `-snapshot-threshold` | 1024 | entries | ≥ 1 |

- `-snapshot-interval` — the interval between snapshot necessity
  checks: at least once per interval the node checks the snapshot
  threshold.
- `-snapshot-threshold` — the minimum number of log entries since the
  last snapshot that triggers a new snapshot.

### Addresses and Cluster

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-data-dir` | "" → `data/node-N` (cmd/main.go) | path | — |
| `-http-addr` | `:8880` | address | — |
| `-rpc-addr` | `:9990` | address | — |
| `-max-pool` | 4 (DefaultMaxPool) | connections | ≥ 0; 0 is replaced with 4 |
| `-number` | −1 | node number | — |
| `-peers` | "" | `id=host:port,...` | fail-fast parsing |

- `-data-dir` — the directory of the node's persistent storage (log
  and snapshots); when empty, the path `data/node-N` is used, where N
  is the value of the `-number` flag.
- `-http-addr` — the address of the node's HTTP interface.
- `-rpc-addr` — the address of the node's RPC server.
- `-max-pool` — the maximum number of pooled connections per peer
  address. A negative value aborts startup; zero is replaced with the
  node default 4 (`DefaultMaxPool`, `cmd/main.go`). The transport's
  own default (2 connections, `pkg/raft/transp`) is not used when
  starting the node — the zero is replaced before the transport is
  created.
- `-number` — the node number; passed to the configuration as the node
  identifier (ServerID) and used in the default data path.
- `-peers` — the cluster peer list: `id=host:port` items separated by
  commas. The `rpc://` scheme prefix is accepted; without a scheme the
  address is treated as an RPC address. A parse error of any item
  aborts startup.

### Tracing

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-trace-cm-log-file` | "" = stderr | path | — |
| `-trace-kv-log-file` | "" = stderr | path | — |
| `-trace-log-level` | 1 | level | — |

- `-trace-cm-log-file` — the consensus module trace log file; an empty
  value means the standard error stream.
- `-trace-kv-log-file` — the KV service trace log file; an empty value
  means the standard error stream.
- `-trace-log-level` — the threshold of trace debug messages for the
  `raft` and `kvservice` packages (internal/config/config.go). The
  binary default is 1; the stand value differs (see the section "Node
  Profile vs Stand Profile").

### Profiling

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-pprof-addr` | "" = disabled | address | — |
| `-block-profile-rate` | 0 | events | — |
| `-mutex-profile-fraction` | 0 | events | — |

- `-pprof-addr` — the address of a dedicated profiling HTTP server;
  when empty, no server is created.
- `-block-profile-rate` — the fraction of blocking events sampled;
  block profiling is enabled by a value greater than zero
  (`runtime.SetBlockProfileRate`).
- `-mutex-profile-fraction` — the fraction of mutex contention events
  sampled; enabled by a value greater than zero
  (`runtime.SetMutexProfileFraction`).

## Timing Invariants

The four timing flags are validated at startup by
`raft.ValidateTiming`: individual ranges, a whole number of
milliseconds, and cross-parameter requirements. All violations are
collected into a single error (`errors.Join`) and reported as one
message of the form `invalid Raft timing flags: …`; each violation
names the flag, the actual value, and the requirement (for example:
`heartbeat-timeout must be between 5ms and 82ms, got 90ms`).

| Parameter | Range | Cross-parameter requirement |
|---|---|---|
| `heartbeat-timeout` | [5 ms; 82 ms], whole ms | upper bound 82 ms = ⌊330/4⌋ — from the fixed 330 ms quorum check |
| `ticker-timeout` | [1 ms; 1000 ms], whole ms | ≤ reelection-timeout/10 |
| `reelection-timeout` | [200 ms; 30 000 ms], whole ms | ≥ 10·heartbeat-timeout |
| `apply-batch-interval` | [1 ms; 5000 ms], whole ms | ≤ reelection-timeout |

Cross-parameter invariants:

- `reelection-timeout` ≥ 10·heartbeat-timeout — within the reelection
  base the leader must be able to deliver ten heartbeats;
- `ticker-timeout` ≤ reelection-timeout/10 — the tick must not distort
  the election timeout by more than a tenth of the base;
- `apply-batch-interval` ≤ reelection-timeout — the apply batch must
  not delay the node's reaction longer than the reelection base.

Examples:

- A valid combination: `-heartbeat-timeout=40ms
  -reelection-timeout=450ms` — 450 ≥ 10·40 = 400 and 40 ≤ 82 hold;
  with the default ticker of 20 ms and batch of 50 ms, 20 ≤ 450/10 =
  45 and 50 ≤ 450 also hold.
- An invalid value: `-heartbeat-timeout=90ms` — above the upper range
  bound of 82 ms.
- An invalid pairing: `-heartbeat-timeout=40ms` with the default
  `-reelection-timeout=340ms` — violates 340 ≥ 10·40 = 400.

Besides the timing flags, startup rejects immediately: a
non-positive `-tcp-rpc-timeout`, a negative `-max-pool`, a
non-positive `-snapshot-interval`, and a `-snapshot-threshold` below 1
(`internal/config/config.go`).

## Node Profile vs Stand Profile

The `make start-raft` stand passes the nodes values that differ from
the binary defaults (`Makefile:12–29`). Do not mix the three sets of
numbers: the binary defaults, the stand profile, and the transport
default.

| Parameter | Node flag default | Stand value (Makefile) |
|---|---|---|
| Heartbeat period (`-heartbeat-timeout` / `HEARTBEAT_TIMEOUT`) | 33 ms | 45 ms |
| Election timeout base (`-reelection-timeout` / `REELECTION_TIMEOUT`) | 340 ms | 500 ms |
| Election ticker tick (`-ticker-timeout` / `TICKER_TIMEOUT`) | 20 ms | 20 ms |
| Apply batch interval (`-apply-batch-interval` / `APPLY_BATCH_INTERVAL`) | 50 ms | 50 ms |
| Trace level (`-trace-log-level` / `TRACE_LOG_LEVEL`) | 1 | 0 |

The stand profile is valid: 500 ≥ 10·45 = 450, 45 ≤ 82, 20 ≤ 500/10 =
50, 50 ≤ 500.

Running the stand with the node defaults (the full set of variables —
so that the recipe does not depend on the current values of the
`Makefile` variable block):

```bash
make start-raft APPLY_BATCH_INTERVAL=50 HEARTBEAT_TIMEOUT=33 \
  REELECTION_TIMEOUT=340 TICKER_TIMEOUT=20
```

Raising the heartbeat is bounded from above by the invariant
`reelection-timeout ≥ 10·heartbeat-timeout`: for a given reelection
base the heartbeat ceiling equals reelection-timeout/10. With the node
default of 340 ms the ceiling is 34 ms; with the stand value of
500 ms it is 50 ms (the stand heartbeat of 45 ms keeps a 5 ms margin).
The range's own upper bound of 82 ms applies independently of this.

## loadkv Load Generator Flags

The `loadkv` generator accepts 10 flags. Only `-request-rate` is
checked at startup; how the operation shares are reconciled is
described in the subsection "Share Semantics".

| Flag | Default | Unit | Bounds |
|---|---|---|---|
| `-concurrency` | 4 | requests | — |
| `-delete-percent` | 0 | % | share ladder |
| `-duration` | 0 = until signal | duration | — |
| `-get-percent` | 66 | % | share ladder |
| `-key-count` | 2000 | keys | — |
| `-peers` | "" | `host:port,...` (HTTP) | — |
| `-request-rate` | 100 | 1/s | ≥ 1, otherwise rejected (cmd/load/main.go) |
| `-value-size` | 128 | bytes | — |
| `-verify-percent` | 33 | % | — |
| `-weak-get-percent` | 0 | % | share ladder |

- `-concurrency` — the maximum number of requests in flight.
- `-duration` — the run duration; zero means run until the stop signal
  (keyboard interrupt or SIGTERM).
- `-key-count` — the number of distinct keys each operation chooses
  from.
- `-peers` — cluster node addresses separated by commas; the `http://`
  prefix is accepted, and without a scheme the address is treated as
  an HTTP address. The "(HTTP)" note in the unit column indicates the
  generator's address protocol, not a description of the HTTP
  interface (which is covered by a separate document).
- `-request-rate` — the request start rate; a value below 1 is
  rejected at startup with the message `invalid -request-rate: must be
  >= 1`.
- `-value-size` — the value size in bytes for write operations.
- `-verify-percent` — the share of write and delete operations after
  which the key is re-read (see below).

### Share Semantics

The operation kind is chosen by the "share ladder": for a random
number r from [0; 100) the bands are checked:

- [0, d) — DELETE, where d = delete-percent;
- [d, d+w) — weak read, where w = weak-get-percent;
- [d+w, d+w+g) — GET, where g = get-percent;
- the remainder [d+w+g; 100) — PUT.

When the sum of shares exceeds 100, the last reached band is truncated
(for d > 100 — DELETE, for d+w > 100 — the weak read, otherwise GET),
and all subsequent bands and PUT are empty. Share values outside
[0; 100] are not supported: a negative share shifts all subsequent
boundaries down.

`-verify-percent` does not participate in the ladder: after a
successful write (and after a successful delete), with probability
verify-percent the key is re-read with a weak read to check the
result.

## Mapping to Makefile Variables

The stand variables are passed to the flags by the `start-raft` target
and are overridden on the make command line without editing the file.
Node timing parameters are given as a number of milliseconds — the
`ms` suffix is appended by the target's recipe.

| Variable | Flag | Makefile default |
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
| `STAND` | — (prefix of stand artifact file names) | empty |

The stand defaults of the remaining generator flags differ from the
binary defaults: `CONCURRENCY` 8 versus 4, `GET_PERCENT` 75 versus 66,
`REQUEST_RATE` 200 versus 100. The `-key-count` flag is not passed by
the `start-raft` target — the binary default applies.

## Version synchronization

This English document is a mirror of the Russian `docs/Flags.md`. The
Russian version is the primary source of truth: any change to a fact
(a flag name, default, unit, or bound) must be made in the Russian
version and mirrored here within the same change; changing only the
English version is not allowed, except to improve the quality of the
translation. The flag names in the first table column and the numeric
facts of every row are kept in sync between the two files.
