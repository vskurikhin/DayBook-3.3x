# Consensus Module API Reference (package `raft`)

## Purpose

The root package `raft` implements the Raft consensus protocol (version 3):
leader election with pre-vote, log replication, entry commitment, cluster
membership changes, state machine snapshots, and leader verification without
writing to the log. The package is used by the KV service
(`pkg/kvservice`) and the executables under `cmd/...`.

## 1. Constants

| Name | Value | Purpose |
|---|---|---|
| `ProtocolVersion` | 3 | Raft protocol version. Compatibility with versions 0–2 is not supported. |
| `TCPRPCTimeout` | 165 ms | TCP RPC timeout in the production transport; 330/2, kept as a separate constant so that test-time timer acceleration does not affect the production transport. |
| `DefaultApplyBatchInterval` | 50 ms | Interval at which the leader loop checks whether entries must be applied to the state machine; accumulated commits are merged into a single batch. |
| `DefaultHeartbeatTimeout` | 33 ms | Default leader heartbeat period. |
| `DefaultReelectionTimeout` | 340 ms | Default election timeout base: the actual timeout is derived from it by a random value. |
| `DefaultTickerTimeout` | 20 ms | Default election ticker tick. |
| `LeaktestBudget` | 600 ms | Unified goroutine leak-check budget: max(in-memory RPC timeout, TCPRPCTimeout) + 100 ms. |
| `DefaultSnapshotInterval` | 3 s | Interval of snapshot necessity checks. |
| `DefaultSnapshotThreshold` | 1024 | Minimum number of entries after the last snapshot that triggers a new one; a compromise between FSM load and recovery time. |

Admissible timer bounds (`Min*`/`Max*`) — see section 2.

Log entry types — values of `LogType`: `LogCommand` (client command),
`LogNoop` (leader's no-op entry), `LogConfiguration` (cluster membership
change).

Suffrage — values of `ServerSuffrage`: `Voter` (voting server), `Nonvoter`
(non-voting; cannot be elected leader).

## 2. Timer bounds and invariants

```go
func ValidateTiming(tc TimerConfig) error
```

`ValidateTiming` validates the node's timing parameters: individual bounds
of every parameter (range and a whole number of milliseconds). The function
is pure, without side effects; all violations are collected into a single
error (`errors.Join`).

Cross-parameter invariants:

- `Reelection >= 10 * Heartbeat` — a follower does not start elections
  between heartbeats of a live leader;
- `Ticker <= Reelection / 10` — the tick defines the quantization error of
  the election timeout;
- `ApplyBatch <= Reelection`.

Every value must be a whole number of milliseconds; a zero or negative
`TimerConfig` field means the default of the corresponding `Default*`
constant.

## 3. Errors of the raft package

| Name | Message | When returned |
|---|---|---|
| `ErrNotLeader` | `raft: not leader` | A leader operation (`Apply`, `VerifyLeader`, `LeadershipTransfer`, a membership change) is invoked on a node that is not the leader. |
| `ErrLeadershipLost` | `raft: leadership lost while committing` | Leadership was lost before commitment: pending futures are resolved with this error when the leader steps down to a follower. |
| `ErrUnsupportedProtocol` | `raft: unsupported protocol version` | An incoming RPC uses a protocol version other than 3 (`checkRPCHeader`). |
| `ErrLeadershipTransferInProgress` | `raft: leadership transfer in progress` | `Apply` during a leadership transfer; a repeated `LeadershipTransfer` while one is already in progress. |
| `ErrTooManyUncommittedEntries` | `raft: too many uncommitted log entries` | The leader's uncommitted log tail reached the limit (4096): the quorum is unavailable or peers cannot keep up with the load. |
| `ErrNothingNewToSnapshot` | `raft: nothing new to snapshot` | There are no new committed entries to create a snapshot from. |
| `ErrBatchFSMResponseMismatch` | `raft: ApplyBatch response count mismatch` | `BatchingFSM.ApplyBatch` returned a number of responses not equal to the number of entries; the error is delivered to all futures of the batch via `ApplyFuture.Error()`. |

## 4. Marker errors contract.Err*

Protocol marker errors are declared in `pkg/raft/contract` and used by
consumers directly with the package qualifier. They are NOT re-exported into
the root package: a `var` re-export would create a second instance of the
error, breaking `err == ErrX` and `errors.Is` comparisons between the root
and leaf packages.

Complete inventory (taken from `go doc -all ./pkg/raft/contract`,
VARIABLES section; the package has no other marker errors):

| Name | Message | When returned |
|---|---|---|
| `contract.ErrEnqueueTimeout` | `raft: timeout enqueuing operation` | Timed-out enqueueing of an operation (e.g., `Apply` with a full `applyCh`). |
| `contract.ErrNotImplemented` | `raft: not implemented` | RPC methods not implemented yet (`TimeoutNow`, `InstallSnapshot`, `AppendEntriesPipeline` in the transport). |
| `contract.ErrNotReachable` | `raft: peer not reachable` | An RPC send attempt to a peer that is not connected to or disconnected from this transport. |
| `contract.ErrRaftShutdown` | `raft: raft is shutdown` | Operations of a stopped Raft node (after `Stop`/`Shutdown`), including RPC sends over a closed transport. |

## 5. Constructor and lifecycle

```go
func NewConsensusModule(
	id int,
	peerIds []int,
	transport Transport,
	storage Storage,
	fsm FSM,
	ready <-chan any,
	snapshots ...SnapshotStore,
) *ConsensusModule
```

`NewConsensusModule` creates a new consensus module instance.
Precondition: `transport != nil` — on violation the function terminates
immediately under the fail-fast strategy (panic); the check also catches a
typed nil pointer in the interface. This is the only validation performed
before goroutines start: restoration from storage and snapshots happens in
the constructor before goroutines are launched, and elections begin only
after the `ready` channel is closed.

```go
func (cm *ConsensusModule) Report() (id, term int, isLeader bool)
```

`Report` returns a report of the node's state: identifier, current term,
and the leader flag; an observation point without side effects.

```go
func (cm *ConsensusModule) Stop()
```

`Stop` stops the module: sets the `Dead` state, closes the shutdown
channel, and waits for all goroutines (`wg.Wait()`). The method is
idempotent: repeated and concurrent calls are safe; there is no early
return without waiting for the goroutines.

## 6. Roles and transitions

The state machine `CMState` — `Follower`, `PreCandidate`, `Candidate`,
`Leader`, `Dead` (terminal; set by `Stop`). The `String()` method returns
the state name.

```
Follower ──election timeout──▶ PreCandidate ──PreVote quorum──▶ Candidate ──RequestVote quorum──▶ Leader
   ▲                              │ quorum lost/higher term       │ higher term                    │
   └──────────────────────────────┴───────────────────────────────┴────────────────────────────────┘
                                     any state ── Stop() ──▶ Dead
```

Elections are implemented in `raft_cm_election.go`:

- **Follower → pre-candidate.** When the election timeout expires without
  messages from a leader, the timer `runElectionTimer` starts
  `runPreCandidate`. The node enters `PreCandidate` WITHOUT incrementing
  its own term and WITHOUT persisting state: the pre-vote does not increase
  the node's term and does not reset the peers' timers, so a partitioned
  node cannot disrupt the cluster.
- **Collecting replies.** `collectPreVoteReplies` gathers RequestPreVote
  replies; a quorum of granted replies leads to
  `startElectionAfterPreVote` — the node waits a random pause (up to 50 ms,
  so that several nodes do not start elections simultaneously) and starts
  the real election. Losing the quorum or a higher term returns the node to
  `Follower` via `becomeFollowerLocked`.
- **Candidate.** `startElectionLocked` increments the term, votes for
  itself, and sends `RequestVote` to every voter. A quorum of votes
  transitions the node to `Leader`; a single-node cluster wins immediately.
- **Stepping down.** On a higher term from another node (in RequestVote and
  RequestPreVote replies — `becomeFollowerLocked`) the node becomes a
  follower; on pre-vote timeout without a quorum it also returns to a
  follower.
- **Relation to timer bounds.** The invariant
  `Reelection >= 10 * Heartbeat` guarantees that a follower does not start
  elections between heartbeats of a live leader; the random election
  timeout `electionTimeoutLocked` returns a duration from the range
  [base; 2·base) — the built-in protection against simultaneous no-quorum
  elections (split vote).
- **Observation.** `Report()` returns `(id, term, isLeader)` — an external
  observation point of the state machine.

## 7. Client path

```go
func (cm *ConsensusModule) Apply(command any, timeout time.Duration) ApplyFuture
func (s *Server) Apply(cmd any, timeout time.Duration) ApplyFuture
```

`Apply` submits a command to Raft and returns an `ApplyFuture`; the command
is applied to the state machine after commitment. The `timeout` argument
bounds only the enqueueing into the `applyCh` channel: on expiry a future
with the `contract.ErrEnqueueTimeout` error is returned. On a non-leader —
`ErrNotLeader`; during a leadership transfer —
`ErrLeadershipTransferInProgress`; after the node is stopped —
`contract.ErrRaftShutdown`. The command is stored by reference: the caller
must not mutate it after the call.

```go
func (cm *ConsensusModule) VerifyLeader() Future
func (s *Server) VerifyLeader() Future
```

`VerifyLeader` verifies that the node is still the leader using the
ReadIndex mechanism (Raft, §8): quorum confirmation without writing to the
log. On a non-leader a future with `ErrNotLeader` is returned immediately;
the quorum threshold is taken from the voter set of the current
configuration.

```go
func (cm *ConsensusModule) LeadershipTransfer(targetID ServerID) LeadershipTransferFuture
```

`LeadershipTransfer` initiates a graceful leadership transfer to the given
node: checks (the target is not the node itself; the target is a voter; no
transfer is in progress) and enqueueing of the future into the transfer
channel. Returns a future with `ErrNotLeader` on a non-leader and with
`ErrLeadershipTransferInProgress` if a transfer is already in progress; the
result is awaited via `LeadershipTransferFuture`.

## 8. RPC handlers

Handlers of incoming consensus-module RPCs (the signatures are part of the
exported API, aligned with the transport contract):

```go
func (cm *ConsensusModule) AppendEntries(args AppendEntriesArgs, reply *AppendEntriesReply) error
func (cm *ConsensusModule) RequestVote(args RequestVoteArgs, reply *RequestVoteReply) error
func (cm *ConsensusModule) RequestPreVote(args RequestPreVoteArgs, reply *RequestPreVoteReply) error
```

- `AppendEntries` — the leader's request to append log entries (§5.3).
  Durability boundary: the `Success: true` reply is sent only after the
  accepted entries have become durable on the follower — the leader is
  entitled to commit an entry based on such a reply.
- `RequestVote` — vote request (§5.4.1). Candidates that are not voters in
  the current configuration are rejected.
- `RequestPreVote` — pre-vote (§4 Pre-Vote): does not change `votedFor` and
  `currentTerm`, does not persist state, does not convert the node to a
  follower on a higher term; additionally checks whether the recipient
  knows a live leader.

RPC sending is performed by the `Transport` interface (section 13):
transport errors (`contract.ErrNotReachable`, `contract.ErrRaftShutdown`,
`contract.ErrEnqueueTimeout`) are returned in `err`; logical Raft errors
(higher term, log conflict) — in `reply` with `err == nil`.

## 9. Membership

```go
func (cm *ConsensusModule) AddVoter(id ServerID, addr ServerAddress) IndexFuture
func (cm *ConsensusModule) AddNonvoter(id ServerID, addr ServerAddress) IndexFuture
func (cm *ConsensusModule) DemoteVoter(id ServerID) IndexFuture
func (cm *ConsensusModule) RemoveServer(id ServerID) IndexFuture
func (cm *ConsensusModule) GetConfiguration() ConfigurationFuture
```

`AddVoter` adds a voting server or updates an existing one's address;
`AddNonvoter` — a non-voting one; `DemoteVoter` demotes a voter to a
non-voter; `RemoveServer` removes a server from the configuration.
`GetConfiguration` returns the current configuration: latest for the leader,
committed for a follower. Membership changes go through a `LogConfiguration`
entry in the log.

The membership-change command type:

```go
type ConfigurationChangeCommand int

const (
	AddVoter ConfigurationChangeCommand = iota + 1
	AddNonvoter
	DemoteVoter
	RemoveServer
)
```

Configuration encoding for the log (gob format):

```go
func EncodeConfiguration(c Configuration) ([]byte, error)
func DecodeConfiguration(data []byte) (Configuration, error)
```

## 10. Snapshots

```go
func (cm *ConsensusModule) SetSnapshotConfig(threshold, trailing int, interval time.Duration)
```

`SetSnapshotConfig` (`raft_cm_snapshot.go`) updates the snapshot
parameters (entry-count threshold, trailing-log count, check interval) and
signals the snapshot loop to check whether a snapshot is due.

The snapshot infrastructure — transparent aliases from
`pkg/raft/contract` (details in section 13 and the godoc of
`SnapshotStore`):

- `SnapshotStore` — snapshot storage: `Create`, `List`, `Open`;
- `FSMSnapshot` — a state-machine snapshot: `Persist(SnapshotSink) error`,
  `Release()`;
- `SnapshotSink` — the snapshot data sink (`io.WriteCloser` plus `ID`,
  `Cancel`);
- `SnapshotMeta` — snapshot metadata (index, term, configuration, size).

## 11. The Server type

`Server` is a thin wrapper over the transport and the consensus module for
compatibility with `cmd/main.go` and `pkg/kvservice`.

```go
func New(cfg *Config, ready <-chan any) *Server
```

`New` (`server.go`) creates a server from the configuration;
precondition — `cfg.Transport != nil` (panic on violation, same as in
`NewConsensusModule`).

```go
func (s *Server) Serve()
func (s *Server) Apply(cmd any, timeout time.Duration) ApplyFuture
func (s *Server) ConnectToPeer(peerID int, addr net.Addr) error
func (s *Server) ConnectToPeerWithTimeout(peerID int, addr net.Addr, _ time.Duration) error
func (s *Server) DisconnectPeer(peerID int) error
func (s *Server) DisconnectAll()
func (s *Server) GetListenAddr() net.Addr
func (s *Server) IsLeader() bool
func (s *Server) VerifyLeader() Future
func (s *Server) Shutdown()
```

`Serve` creates the consensus module over the transport, normalizes the
timers (values <= 0 are replaced with defaults), validates them with
`ValidateTiming` (immediate failure on violation), and applies the
snapshot parameters. `ConnectToPeerWithTimeout` stores the peer's address
in the transport (connection is lazy, at the first RPC; the timeout
argument is ignored), `ConnectToPeer` is its shorthand. `DisconnectPeer`
disconnects a peer, `DisconnectAll` — all peers. `GetListenAddr` returns
the transport's listen address; `IsLeader` — the leader flag (via
`Report`). `Shutdown` stops the server: `cm.Stop()` (joining the
goroutines), then closing the transport.

## 12. Configuration and contracts

### Config

```go
type Config struct {
	ApplyBatchInterval time.Duration
	Fsm FSM
	HeartbeatTimeout time.Duration
	PeerAddresses map[int]net.Addr
	PeerIds       []int
	ServerID      int
	ReelectionTimeout time.Duration
	SnapshotInterval time.Duration
	SnapshotStore SnapshotStore
	SnapshotThreshold int
	Storage Storage
	TickerTimeout time.Duration
	Transport TransportManager
}
```

Configuration for creating a new server. Timer fields and snapshot
parameters with value 0 are replaced with defaults; `SnapshotStore == nil`
disables snapshots; `Transport` is created by the caller (precondition of
`New`/`Serve`).

### TimerConfig

```go
type TimerConfig struct {
	ApplyBatch time.Duration
	Heartbeat time.Duration
	Reelection time.Duration
	Ticker time.Duration
}
```

The node's timing parameters; a zero or negative value means the default of
the corresponding `Default*` constant. Validated by `ValidateTiming`.

### TraceConfig and SetTrace

```go
type TraceConfig struct {
	Level int
	LogFile string
}
func SetTrace(cfg TraceConfig) error
```

`SetTrace` configures consensus-module tracing: the verbosity threshold
`Level` (0 — disabled) and the output destination `LogFile` (empty string —
the standard logger). Strict set-once contract: a successful call is
possible exactly once per process and only before the first consensus
module is created; any repeated call (including after module creation) is a
deterministic error. An I/O error does not consume the configuration
window.

### CommitEntry

```go
type CommitEntry struct {
	Data any
	Index int
	Term int
}
```

The data Raft sends to the commit channel: the command, the log index, and
the term of the commitment.

### CMState

The node's role type — see section 6 (`Follower`/`PreCandidate`/`Candidate`/
`Leader`/`Dead`, method `String()`).

### CommitChannelFSM (test-only)

```go
type CommitChannelFSM struct{ /* ... */ }
func NewCommitChannelFSM(commitChan chan<- CommitEntry) *CommitChannelFSM
func (f *CommitChannelFSM) Apply(log *LogEntry) any
func (f *CommitChannelFSM) Restore(_ io.ReadCloser) error
func (f *CommitChannelFSM) Snapshot() (FSMSnapshot, error)
```

A wrapper turning a commit channel into an `FSM` for backward compatibility
with the test infrastructure: it forwards `log.Data` to the channel.
Test-only: while the node is alive the channel must have a reader (`Apply`
must not block forever, otherwise `Stop` hangs).

## 13. Interfaces

```go
type FSM interface {
	Apply(*LogEntry) any
	Snapshot() (FSMSnapshot, error)
	Restore(io.ReadCloser) error
}
```

The client's state machine: `Apply` applies a committed entry, `Snapshot`
returns a state snapshot (fast capture; serialization happens in
`FSMSnapshot.Persist`), `Restore` restores the state from a snapshot,
resetting the previous one. Entries are passed as owned copies; the payload
must not be mutated after the call.

```go
type BatchingFSM interface {
	FSM
	ApplyBatch([]*LogEntry) []any
}
```

An optional `FSM` extension for group application of entries: the response
slice length must equal the input length, otherwise all futures of the
batch fail with `ErrBatchFSMResponseMismatch`.

```go
type Storage interface {
	Set(key string, value []byte)
	Get(key string) ([]byte, bool)
	HasData() bool
}
```

A key-value persistent storage provider; `Set` stores a copy, `Get` returns
a defensive copy.

```go
type Transport interface {
	Consumer() <-chan RPC
	AppendEntries(ServerID, AppendEntriesArgs) (AppendEntriesReply, error)
	RequestVote(ServerID, RequestVoteArgs) (RequestVoteReply, error)
	RequestPreVote(ServerID, RequestPreVoteArgs) (RequestPreVoteReply, error)
	TimeoutNow(ServerID, TimeoutNowRequest) (TimeoutNowResponse, error)
	InstallSnapshot(ServerID, InstallSnapshotRequest, io.Reader) (InstallSnapshotResponse, error)
	AppendEntriesPipeline(ServerID) (AppendPipeline, error)
	SetHeartbeatHandler(func(RPC))
	LocalAddr() ServerAddress
}
```

The network-layer abstraction: sending RPCs to peers and receiving incoming
RPCs via `Consumer()`. All methods are safe for concurrent use; after the
transport is closed the methods return `contract.ErrRaftShutdown`.
`AppendEntries` and `RequestVote` are blocking; `RequestPreVote` and
`InstallSnapshot` in the current transport implementation return
`contract.ErrNotImplemented` for the unsupported branches.

```go
type TransportManager interface {
	Transport
	Connect(peerID ServerID, addr string)
	Disconnect(peerID ServerID)
	DisconnectAll()
	Close()
}
```

A transport with connection management: the peer address book and closing.
The implementation is `TCPTransport` in `pkg/raft/transp` (a leaf package,
not covered by this reference).

```go
type AppendPipeline interface {
	AppendEntries(AppendEntriesArgs) (AppendEntriesReply, error)
	Consumer() <-chan RPCResponse
	Close() error
}
```

Pipelined `AppendEntries` sending without waiting for replies; obtaining a
pipeline currently returns `contract.ErrNotImplemented`.

## 14. Future hierarchy

```go
type Future interface {
	Error() error
	ErrorCh() <-chan error
}
type IndexFuture interface {
	Future
	Index() int
}
type ApplyFuture interface {
	IndexFuture
	Response() any
}
type ConfigurationFuture interface {
	IndexFuture
	Configuration() Configuration
}
type LeadershipTransferFuture interface {
	Future
}
```

`Future` — an asynchronous operation: `Error` is the blocking wait for the
error, `ErrorCh` is the error channel for a `select` without extra
goroutines. `IndexFuture` adds the log index; `ApplyFuture` — the state
machine response; `ConfigurationFuture` — the cluster configuration;
`LeadershipTransferFuture` awaits the result of a leadership transfer
(returned by `LeadershipTransfer`, see above).

```go
type SnapshotFuture struct{ /* ... */ }
func (d *SnapshotFuture) Error() error
func (d *SnapshotFuture) ErrorCh() <-chan error
```

The public future of a user-initiated snapshot request.

## 15. RPC structures and domain types

All types listed below are transparent aliases of declarations from
`pkg/raft/contract` (`aliases.go`); the owner is the `contract` package.

| Type | Owner | Purpose |
|---|---|---|
| `RPCHeader` | contract | Common RPC header: protocol version and sender identifier. |
| `WithRPCHeader` | contract | Interface for obtaining the `RPCHeader` from an RPC message. |
| `RequestVoteArgs` / `RequestVoteReply` | contract | Arguments and reply of the vote request. |
| `RequestPreVoteArgs` / `RequestPreVoteReply` | contract | Arguments and reply of the pre-vote. |
| `AppendEntriesArgs` / `AppendEntriesReply` | contract | Arguments and reply of the append-entries call (including the conflict-resolution fields). |
| `InstallSnapshotRequest` / `InstallSnapshotResponse` | contract | Snapshot installation request and reply. |
| `TimeoutNowRequest` / `TimeoutNowResponse` | contract | Immediate-election request and reply (leadership transfer). |
| `RPC` | contract | Incoming RPC request from `Consumer()`: command, data stream, reply channel. |
| `RPCResponse` | contract | RPC response: `Reply` or `Error`. |
| `LogEntry` | contract | Log entry: index, term, type, data. |
| `LogType` | contract | Log entry type (see section 1). |
| `Configuration` | contract | Cluster composition. |
| `ConfigServer` | contract | One server of the configuration: identifier, address, suffrage. |
| `ServerSuffrage` | contract | Suffrage: `Voter`/`Nonvoter`. |
| `ServerID` | contract | Server identifier. |
| `ServerAddress` | contract | Server address (string). |

## 16. Utilities

```go
func ValidateTiming(tc TimerConfig) error
func EncodeConfiguration(c Configuration) ([]byte, error)
func DecodeConfiguration(data []byte) (Configuration, error)
func SetTrace(cfg TraceConfig) error
func IsNilInterface(v any) bool
func RandomInt(m int64) (int64, error)
```

- `ValidateTiming` — timer validation (section 2);
- `EncodeConfiguration` / `DecodeConfiguration` — configuration encoding to
  and from gob (section 9);
- `SetTrace` — tracing configuration (section 12);
- `IsNilInterface` — true for an empty interface and for a typed nil (nil
  pointer, nil channel, etc.), detected via reflection; used as the
  precondition of `NewConsensusModule` and `New`;
- `RandomInt` — a uniformly distributed integer from a range.

## Version synchronization

The Russian version (`docs/API-ConsensusModule.md`) is the primary one. The
English twin `docs/API-ConsensusModule-en.md` is updated in the same task as
any change of facts in the Russian version (a signature, a constant, an
error message, a bounds table). Changing only the English version without
changing the Russian one is not allowed, except for translation-quality
fixes.
