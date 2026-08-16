# Go Reticulum and LXMF ecosystem assessment

Assessment date: 2026-08-15.

## Recommendation

Use **Go -> small Python LXMF bridge -> official Python `rnsd`** for the first production-oriented implementation.

This is not because direct Go interoperability is absent. Quad4's current Reticulum-Go and `reticulum-go-protocols/pkg/lxmf` are credible, actively maintained implementations with explicit Python cross-reference and live interoperability tests. The bridge is recommended because it keeps the cryptographic and LXMF state machine on the current reference implementation, exposes richer delivery callbacks, and gives the web application a stable private API while the Go ecosystem is still pre-1.0 or newly 1.0.

The bridge must share the Reticulum container's network namespace. The official Python TCP shared-instance server and client both hard-code `127.0.0.1`; configuring port `37428` does not make it listen on a Podman bridge address. Port `37429` is the authenticated management RPC port. Live testing showed that reliable direct LXMF links use it for first-hop timeout and packet PHY metadata queries. Since separate config directories derive different default keys, both containers must configure the same explicit `rpc_key`.

## Libraries evaluated

### Quad4 Reticulum-Go and reticulum-go-protocols

- Repositories: <https://github.com/Quad4-Software/Reticulum-Go> and <https://github.com/Quad4-Software/reticulum-go-protocols>.
- Maintenance: very active. The inspected default branches had commits dated 2026-08-14 and 2026-08-15. Reticulum-Go has tagged releases, and the protocols repository tracks LXMF 1.1.0.
- Shared instance: implemented in `pkg/sharedinstance` and `pkg/interfaces`. TCP uses port `37428` and loopback, matching Python. The management RPC implementation supports port `37429` and Python's msgpack/multiprocessing-connection protocol.
- Identities: implemented, including Python-compatible identity and destination behavior.
- LXMF send/receive: implemented by `pkg/lxmf`, including opportunistic and direct delivery, propagation support, stamps, announce metadata, containers, and delivery destinations.
- Interoperability: the repositories include Python-generated cross-reference vectors and live Go/Python tests. The LXMF repository specifically includes Python-to-Go delivery tests and wire-format round trips.
- Announces and paths: supported by Reticulum-Go; the LXMF examples request paths, recall announced identities, and subscribe to incoming messages.
- Delivery state: LXMF state constants exist and send paths expose `sent`; direct proof handling exists lower in the stack. The high-level messenger API inspected does not yet offer the reference `LXMessage.register_delivery_callback()` / `register_failed_callback()` lifecycle used by Python's `LXMRouter`, so application-grade state tracking would require additional integration work.
- Limitations: the protocols module is not published under its declared module path on the public Go proxy and requires `replace` directives; it currently requires a very recent Go toolchain. Some Reticulum discovery features remain partial according to its compatibility document.

Verdict: technically capable of attaching directly to Python `rnsd` and exchanging LXMF with Python clients, but still a higher integration and upgrade risk for the first PEREVIA release.

### svanichkin/go-reticulum and svanichkin/go-lxmf

- Repositories: <https://github.com/svanichkin/go-reticulum> and <https://github.com/svanichkin/go-lxmf>.
- Maintenance: active single-maintainer parity ports; inspected commits were dated 2026-04-28. The projects explicitly describe themselves as work in progress and note substantial AI assistance.
- Shared instance: TCP and Unix shared-instance modes are implemented. Source tests cover `shared_instance_type = tcp`, configurable ports, path requests, announces, and shared-instance CLI behavior.
- Identities and LXMF: both are implemented. `go-lxmf` has `LXMRouter`, inbound callbacks, outbound handling, propagation states, and message delivery/failure state transitions.
- Interoperability: the projects target Python behavioral parity, but the inspected test suite provides less explicit live Python LXMF coverage than Quad4's implementation.
- Version gap: the inspected Reticulum port targeted Python RNS 1.1.5 and the LXMF package declared protocol version 0.9.4, while the current official releases inspected were RNS 1.4.2 and LXMF 1.1.0.

Verdict: promising and unusually complete, but the version gap and work-in-progress status make it a weaker choice for this application today.

### Other Go projects

Searches also found older or narrower Go experiments and application-specific message formats. They did not provide stronger evidence for current Python shared-instance plus full LXMF interoperability than the two families above, so they are not recommended as the foundation.

## Capability matrix

| Requirement | Quad4 Go | svanichkin Go | Official Python bridge |
| --- | --- | --- | --- |
| Python TCP shared instance, port 37428 | Yes, loopback | Yes, loopback | Yes, reference behavior |
| Control RPC, port 37429 | Yes | Yes | Yes; explicit shared key required across config dirs |
| Persistent identities | Yes | Yes | Yes |
| Send and receive LXMF | Yes | Yes | Yes |
| Python Sideband interoperability | Explicit live tests and wire vectors | Parity target; less live evidence found | Reference implementation |
| Announces and path discovery | Yes | Yes | Yes |
| Rich delivery callbacks | Partial at high-level API | Implemented | Reference implementation |
| Production maturity | New 1.0 / protocols evolving | Work in progress | Established reference stack |

## Service architecture

```text
Browser / PEREVIA PWA
        |
        | HTTP + SSE/WebSocket
        v
websideband (Go)
  business logic, API, auth, persistence, UI
        |
        | private HTTP + SSE, optional bearer token
        v
lxmf-bridge (Python)
  identity, RNS/LXMF callbacks, send/receive only
        |
        | 127.0.0.1:37428 in shared network namespace
        v
reticulum (existing official rnsd)
  RNode, TCP interfaces, transport, routing
```

Deployment boundaries:

- `reticulum` remains the sole owner of `/dev/rnode` and all physical/network Reticulum interfaces.
- `lxmf-bridge` is a separate unprivileged container started with `--network container:reticulum`. It has no USB device.
- `websideband` is a separate unprivileged container on a private Podman network. It reaches the bridge on the Reticulum container's private network address and is the only service publishing a LAN port.
- The bridge owns the Reticulum identity file because private-key operations belong at the RNS/LXMF boundary. Go stores only the public LXMF address and application records once SQLite is introduced.
- SQLite belongs to `websideband` in the persistence phase. Database access should sit behind repository interfaces so PostgreSQL can be added without changing handlers or domain services.

## Proof-of-concept scope

The first proof provides only:

- persistent LXMF identity and address;
- status and health endpoints;
- direct LXMF text send with path discovery;
- inbound LXMF delivery events;
- announce events and basic hop information;
- outbound queued/sending/sent/delivered/failed events where Python LXMF reports them;
- an SSE stream proxied through the Go service.

It intentionally does not include SQLite, contacts, conversations, authentication for LAN users, or a full UI. Those are subsequent phases after real RF and Sideband interoperability is verified.

## Primary evidence

- Official Reticulum shared-instance configuration and implementation: <https://github.com/markqvist/Reticulum/blob/master/RNS/Reticulum.py> and <https://github.com/markqvist/Reticulum/blob/master/RNS/Interfaces/LocalInterface.py>.
- Official LXMF router and message callbacks: <https://github.com/markqvist/LXMF/blob/master/LXMF/LXMRouter.py> and <https://github.com/markqvist/LXMF/blob/master/LXMF/LXMessage.py>.
- Quad4 compatibility and live interop suites: <https://github.com/Quad4-Software/Reticulum-Go/blob/master/COMPATIBILITY.md> and <https://github.com/Quad4-Software/reticulum-go-protocols/tree/master/pkg/lxmf>.
- svanichkin project status and parity statements: <https://github.com/svanichkin/go-reticulum> and <https://github.com/svanichkin/go-lxmf>.
