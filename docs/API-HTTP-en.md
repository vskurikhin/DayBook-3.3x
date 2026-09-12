# KV Service HTTP API Reference

The KV service HTTP API is the external interface of a replicated
key–value store running on the Raft consensus. The service accepts
HTTP requests, performs read and write operations through the
consensus module, and returns results in JSON. This reference covers
all routes, request and response formats (types of the pkg/api
package), the RespStatus model, transport codes, delivery guarantees,
and the behavior of the stock Go client (the pkg/kvclient package).

Base address: a node listens on the HTTP address set by the
`-http-addr` flag of the raftkv executable. In the demonstration
stand `make start-raft` three nodes listen on ports 8881–8883:
http://localhost:8881, http://localhost:8882, http://localhost:8883.
A request may be sent to any node, but only the leader confirms an
operation (see StatusNotLeader below).

## Routes

Routes are registered with the ServeMux router of the standard
library ("method + path" patterns, Go 1.22+).

| Route                    | Purpose             | Request       | Response       |
|--------------------------|---------------------|---------------|----------------|
| `GET /verifyleader/`     | leader verification | no body       | StatusResponse |
| `GET /weak-get/{key...}` | weak read           | key in path   | GetResponse    |
| `POST /cas/`             | conditional write   | CASRequest    | CASResponse    |
| `POST /delete/`          | deletion            | DeleteRequest | DeleteResponse |
| `POST /get/`             | consensus read      | GetRequest    | GetResponse    |
| `POST /put/`             | write               | PutRequest    | PutResponse    |

## General rules

- The body of a POST request is JSON with the mandatory
  `Content-Type: application/json` header. Parsing is performed by
  readRequestJSON; a missing or differently typed header, malformed
  JSON, a field type mismatch, and unknown body fields
  (DisallowUnknownFields) are parsing errors answered with 400. GET
  routes do not parse a body; the header is not required for them.
- A response carrying the result of an operation is always returned
  with HTTP status 200 and a JSON body; the outcome is conveyed by
  the RespStatus field, not by an HTTP code. The reason: the states
  "not the leader" or "failed to commit the command" have no suitable
  counterparts among standard HTTP codes, therefore every response
  carries its own ResponseStatus status.
- Responses of the GET routes /verifyleader/ and /weak-get/ are
  accompanied by the `Cache-Control: no-store` header: the leadership
  verdict and the read value are current only at the moment of
  leadership confirmation, and caching the response is forbidden.
- Responses are serialized with json.Marshal; a serialization error
  is answered with 500.

## RespStatus statuses

The ResponseStatus type is declared in pkg/api/api.go.

| Value | Name                                | Semantics                                                                                                                                                                                                                                                                                        |
|-------|-------------------------------------|--------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 0     | StatusInvalid / "invalid"           | incompatible operation response                                                                                                                                                                                                                                                                  |
| 1     | StatusOK / "OK"                     | operation completed                                                                                                                                                                                                                                                                              |
| 2     | StatusNotLeader / "NotLeader"       | the operation is not confirmed by this node: the node is not the leader, or the timeout for enqueueing the command into the consensus module queue has expired, or leadership is lost or being transferred, or the node is shutting down; the client should retry the command at another address |
| 3     | StatusFailedCommit / "FailedCommit" | not returned by the handlers; the Go client treats it as "commit failed; retry"                                                                                                                                                                                                                  |

Notes on the table:

- StatusNotLeader is returned for any error of the consensus module
  future, not only for "the node is not the leader": ErrNotLeader —
  the node is not the leader; contract.ErrEnqueueTimeout — the
  timeout for enqueueing the command into the applyCh queue has
  expired; ErrLeadershipTransferInProgress — a leadership transfer is
  in progress; contract.ErrRaftShutdown — the node is shutting down;
  ErrLeadershipLost — leadership is lost; ErrTooManyUncommittedEntries
  — the limit on the uncommitted tail of the log is exceeded.
- StatusInvalid is returned when the future response cannot be cast
  to the command type — a non-standard internal inconsistency; in
  this situation the stock Go client terminates abnormally (the
  section "Go client behavior").
- StatusFailedCommit is a client-side reserve case: it is not
  returned by the HTTP route handlers (the value is not used in
  pkg/kvservice); it is intended for the client side — the Go client
  treats it as "commit failed; retry".

## Transport codes

| Code | When returned                                                                                                                                                                    |
|------|----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------|
| 400  | the Content-Type header is missing or is not application/json; the body is not valid JSON; a field type does not match; the body contains unknown fields (DisallowUnknownFields) |
| 404  | route not found                                                                                                                                                                  |
| 405  | the HTTP method does not match the route pattern                                                                                                                                 |
| 500  | response serialization error (json.Marshal)                                                                                                                                      |

Request context cancellation. If the client cancels the request
context (drops the connection) before the operation completes, the
handler exits without writing a response body: the wait is built on a
select over req.Context().Done(). This holds for the
`GET /weak-get/{key...}` route and all POST routes.

## Guarantees and retries

- Command delivery is at least once: the service does not guarantee
  single execution; a command sent again is executed again. There is
  no command deduplication by request identifier in the service.
- StatusNotLeader does not mean the command will not be committed:
  the entry may already be in the leader's log and become committed
  later. Only the loss of leadership does not cancel an entry already
  appended to the log (ErrLeadershipLost — the future receives the
  error, but the entry remains in the log). With
  contract.ErrEnqueueTimeout and ErrTooManyUncommittedEntries the
  command never enters the log (a return from the timer branch, the
  send into applyCh never happened; the future is answered before the
  entry is appended); the same holds for ErrNotLeader and
  contract.ErrRaftShutdown. Handlers reflect all these future errors
  with the single status StatusNotLeader (ErrNotLeader,
  contract.ErrEnqueueTimeout, ErrLeadershipTransferInProgress,
  contract.ErrRaftShutdown, ErrLeadershipLost,
  ErrTooManyUncommittedEntries).
- The stock Go client, upon StatusNotLeader, switches to another
  address and retries the same command.
- Consequences of retries:
  - PUT — writes the same value again (formally the operation is not
    idempotent if the value changed between retries);
  - CAS — the retry runs anew against the current state (PrevValue
    may differ; the retried CAS may no longer find the previous
    value);
  - DELETE — the retry returns KeyFound=false if the deletion has
    already been committed.

## Routes in detail

### `GET /verifyleader/` — leader verification

- Handler: handleVerifyLeader — pkg/kvservice/kvservice.go.
- Request: no body and no parameters.
- Response: StatusResponse {RespStatus} (pkg/api/api.go): StatusOK —
  the node confirmed leadership, StatusNotLeader — it did not.
- Response header: `Cache-Control: no-store` (kvservice.go) — the
  leadership verdict is current only at the moment of quorum
  confirmation.
- The check is performed without writing to the log — the ReadIndex
  mechanism (section 8 of the Raft paper): the VerifyLeader call
  gathers quorum confirmation from peers against the current state.
- The `/verifyleader/` route does not check request context
  cancellation and has no timeout (the VerifyLeader().Error() call is
  blocking).

### `GET /weak-get/{key...}` — weak read

- Handler: handleWeakGet — pkg/kvservice/kvservice.go.
- The key is passed as the path suffix after the /weak-get/ prefix;
  the {key...} pattern captures the entire remaining path, including
  several slash-separated segments: the request /weak-get/a/b
  corresponds to the key "a/b".
- Request: no body. Response: GetResponse {RespStatus, KeyFound,
  Value}.
- Response header: `Cache-Control: no-store` (kvservice.go) — the
  value is current only at the moment of leadership confirmation.
- Order of work: first leadership is confirmed via VerifyLeader
  without a log write, then the store is read locally with ds.Get. A
  leadership confirmation error yields StatusNotLeader; a missing key
  yields StatusOK with KeyFound=false.
- Request context cancellation — an empty response without a body.
- Key escaping: the client encodes the key with url.PathEscape
  (pkg/kvclient/kvclient.go); the keys "." and ".." are escaped
  manually (%2E and %2E%2E), because PathEscape does not encode the
  dot and dot segments are rewritten by the router.

### POST route mechanics

Every POST route, after parsing the body, performs the same steps:

1. A command is built from the request body; it is submitted to raft
   Apply with the argument _requestTimeout = 10 s.
2. These 10 seconds are the timeout for enqueueing the command into
   the consensus module queue (applyCh): if the command is not
   accepted into the queue within this time, Apply returns
   contract.ErrEnqueueTimeout. This is an enqueue timeout, not a
   commit timeout: the time to append the entry to the log and to
   apply the command is not bounded by it.
3. Waiting for commitment is bounded only by request context
   cancellation: the handler selects between future completion and
   req.Context().Done().
4. Any future error (the list is in "Guarantees and retries") returns
   StatusNotLeader.
5. A future response that cannot be cast to the command type returns
   StatusInvalid.

### `POST /cas/` — conditional write

- Handler: handleCAS — pkg/kvservice/kvservice.go.
- Request: CASRequest (pkg/api/api.go):

  | Field        | Type   | Meaning                           |
  |--------------|--------|-----------------------------------|
  | Key          | string | key                               |
  | CompareValue | string | expected current value of the key |
  | Value        | string | value to write                    |

- Response: CASResponse (pkg/api/api.go):

  | Field      | Type                    | Meaning                                      |
  |------------|-------------------------|----------------------------------------------|
  | RespStatus | ResponseStatus (number) | operation status                             |
  | KeyFound   | bool                    | whether the key existed before the operation |
  | PrevValue  | string                  | key value before the operation               |

- Semantics: if the current value of key Key equals CompareValue,
  Value is written; KeyFound reports whether the key existed, and
  PrevValue its value before the operation. The command is committed
  through the log (consensus).

### `POST /delete/` — deletion

- Handler: handleDelete — pkg/kvservice/kvservice.go.
- Request: DeleteRequest (pkg/api/api.go)
- Response: DeleteResponse (pkg/api/api.go):

  | Field      | Type                    | Meaning                                      |
  |------------|-------------------------|----------------------------------------------|
  | RespStatus | ResponseStatus (number) | operation status                             |
  | KeyFound   | bool                    | whether the key existed before the operation |
  | PrevValue  | string                  | key value before the operation               |

- Semantics: deletes the key; KeyFound and PrevValue describe the
  state before the deletion. The command is committed through the log
  (consensus).

### `POST /get/` — consensus read

- Handler: handleGet — pkg/kvservice/kvservice.go.
- Request: GetRequest (pkg/api/api.go).
- Response: GetResponse (pkg/api/api.go):

  | Field      | Type                    | Meaning                |
  |------------|-------------------------|------------------------|
  | RespStatus | ResponseStatus (number) | operation status       |
  | KeyFound   | bool                    | whether the key exists |
  | Value      | string                  | key value              |

- Semantics: a read through the log — the command passes consensus
  and is applied to the state machine, so the result is consistent
  with other operations. Unlike the weak read
  `GET /weak-get/{key...}`, the value is confirmed by a quorum.

### `POST /put/` — write

- Handler: handlePut — pkg/kvservice/kvservice.go.
- Request: PutRequest (pkg/api/api.go):

  | Field | Type   | Meaning        |
  |-------|--------|----------------|
  | Key   | string | key            |
  | Value | string | value to write |

- Response: PutResponse (pkg/api/api.go):

  | Field      | Type                    | Meaning                                      |
  |------------|-------------------------|----------------------------------------------|
  | RespStatus | ResponseStatus (number) | operation status                             |
  | KeyFound   | bool                    | whether the key existed before the operation |
  | PrevValue  | string                  | key value before the operation               |

- Semantics: stores Value under key Key; KeyFound and PrevValue
  describe the state before the write. The command is committed
  through the log (consensus).

## Go client behavior (pkg/kvclient)

The stock Go client of the pkg/kvclient package operates with a list
of node addresses and retries an operation until it is confirmed:

- assumedLeader — the index of the address the client currently
  assumes to be the leader (pkg/kvclient/kvclient.go); the index is
  read and updated atomically.
- Upon StatusNotLeader or a request error (including a timeout), the
  client switches to the next address in the list and retries the
  same command (kvclient.go — POST routes; weak read; leader
  verification).
- Responses 405 and 404 in GET paths are deterministic errors: the
  method and route are the same for every node, so switching
  addresses is pointless.
- The timeout of a single HTTP request is 500 ms by default; it is
  configurable with the NewWithTimeout constructor.
- The client treats StatusFailedCommit as "commit failed; retry" and
  returns an error.
- A status other than StatusOK, StatusNotLeader, StatusFailedCommit
  (in particular StatusInvalid) drives the stock Go client into a
  panic("unreachable") — the caller's process terminates abnormally;
  the server is capable of returning StatusInvalid.
- Keys of the weak read are encoded with url.PathEscape, including
  keys containing a dot (the special cases "." and ".." — manually).

## Request examples

The examples target the `make start-raft` stand; the nodes in the
examples are 8881–8883; assumption: node 8881 is considered the
leader. The numeric value of RespStatus is given in the comment for
each call.

```bash
# Leader verification — answer of the leader node
curl -s http://localhost:8881/verifyleader/
# {"RespStatus":1}

# Leader verification — answer of a follower node
curl -s http://localhost:8882/verifyleader/
# {"RespStatus":2}

# Weak read of the value of key my-key (the key is the path suffix)
curl -s http://localhost:8881/weak-get/my-key
# {"RespStatus":1,"KeyFound":true,"Value":"v-1"}

# Write the pair my-key=v-1 (the key does not exist yet, so KeyFound=false)
curl -s -X POST http://localhost:8881/put/ \
  -H 'Content-Type: application/json' \
  -d '{"Key":"my-key","Value":"v-1"}'
# {"RespStatus":1,"KeyFound":false,"PrevValue":""}

# Consensus read
curl -s -X POST http://localhost:8881/get/ \
  -H 'Content-Type: application/json' \
  -d '{"Key":"my-key"}'
# {"RespStatus":1,"KeyFound":true,"Value":"v-1"}

# Conditional write: the current value v-1 is replaced with v-2
curl -s -X POST http://localhost:8881/cas/ \
  -H 'Content-Type: application/json' \
  -d '{"Key":"my-key","CompareValue":"v-1","Value":"v-2"}'
# {"RespStatus":1,"KeyFound":true,"PrevValue":"v-1"}

# Delete the key
curl -s -X POST http://localhost:8881/delete/ \
  -H 'Content-Type: application/json' \
  -d '{"Key":"my-key"}'
# {"RespStatus":1,"KeyFound":true,"PrevValue":"v-2"}

# Write request to a follower node: the operation is not confirmed
# by this node; the client should retry the command at another address
curl -s -X POST http://localhost:8883/put/ \
  -H 'Content-Type: application/json' \
  -d '{"Key":"my-key","Value":"v-3"}'
# {"RespStatus":2,"KeyFound":false,"PrevValue":""}
```

## Version synchronization

This English document is a translation of the primary Russian
version, docs/API-HTTP.md; the Russian version is the source of
truth. Any change to a fact (route, JSON field, status code,
transport code, timeout, table row) must be applied to both versions
within the same change. Changing only the English version is not
allowed, except to improve the quality of the translation.
