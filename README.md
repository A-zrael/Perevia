# PEREVIA

A self-hosted, browser-facing LXMF messaging client built primarily in Go and designed to use an existing official Python `rnsd` gateway.

The repository currently contains the Phase 1 assessment, an end-to-end messaging implementation, SQLite persistence, durable media storage, authenticated sessions, optional built-in TLS, and a mobile-first browser interface. The UI includes shareable/scannable identity QR codes, contact search, announce-to-contact actions, a configurable LXMF display name, durable unread counters, privacy-controlled browser notifications, and PWA installation guidance. Browser-local state is retained only as a temporary offline cache.

## Current architecture

```text
Browser -> websideband (Go HTTP/SSE) -> lxmf-bridge (official Python RNS/LXMF) -> existing rnsd
```

- `websideband` owns the public HTTP API, SQLite database, attachment storage, business logic, and UI. Authentication remains a later phase.
- `lxmf-bridge` owns only Reticulum identity operations and LXMF integration.
- the existing `reticulum` service remains the only container with physical interfaces or USB access.

The integration decision and library evidence are in [docs/research/go-reticulum-ecosystem.md](docs/research/go-reticulum-ecosystem.md). Podman commands are in [docs/deployment/podman-proof-of-concept.md](docs/deployment/podman-proof-of-concept.md).

## Proof-of-concept API

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/healthz` | Go and bridge health |
| `GET` | `/api/v1/status` | connection state and our LXMF address |
| `GET/POST` | `/api/v1/auth/{status,setup,login,logout}` | first-run login setup and authenticated sessions |
| `GET` | `/api/v1/messages` | durable conversation history and media URLs |
| `POST` | `/api/v1/messages` | queue a direct or opportunistic text/voice message |
| `DELETE` | `/api/v1/conversations/{address}` | delete conversation history and orphaned stored media |
| `PUT` | `/api/v1/conversations/{address}/read` | persist the conversation read cursor and clear unread state |
| `GET/POST` | `/api/v1/contacts` | list and save durable contacts |
| `DELETE` | `/api/v1/contacts/{address}` | remove a durable contact |
| `GET` | `/api/v1/qr?address=…` | generate a scannable LXMF address QR code |
| `POST` | `/api/v1/qr/decode` | read an LXMF address from a bounded QR image |
| `POST` | `/api/v1/audio/transcode` | convert a bounded browser recording to Sideband-compatible Opus/Ogg |
| `POST` | `/api/v1/images/prepare` | resize, strip metadata, and convert an image to Sideband-compatible WebP |
| `POST` | `/api/v1/announce` | announce our LXMF delivery destination |
| `PUT` | `/api/v1/settings/identity` | persist, apply, and announce a new display name |
| `GET` | `/api/v1/events` | SSE stream for messages, states, and announces |

Open `/` for the Conversations, Contacts, Network, and Settings interface. It loads without Reticulum connectivity and shows a disconnected state until the bridge is configured.

Conversation view includes voice notes up to 60 seconds. Tap the microphone to record and tap it again to stop. Browser recordings are converted by a fixed server-side FFmpeg pipeline and sent as the official LXMF `FIELD_AUDIO` with `AM_OPUS_OGG`. Microphone access works on `localhost`; access from another device requires HTTPS because browsers block microphone capture on insecure LAN origins.

The image button opens the mobile camera/gallery picker. Images are bounded to 12 MB input, stripped of metadata, resized within 1024×1024, compressed to WebP, and sent using the official LXMF `FIELD_IMAGE` structure. Existing composer text is used as the image caption. Incoming Sideband WebP, PNG, and JPEG image fields render inline.

Settings includes a deployment configurator for web and bridge listen IPs, the bridge host/IP, web/bridge/shared/control ports, bridge token, and RNS RPC key. It generates matching `rnsd`, bridge, and Go environment snippets. Secret values remain in page memory only and are never submitted or persisted by the prototype.

SQLite data and private image/audio files live in `/data` inside the Go container. The Podman deployment mounts the `websideband-data` volume there. Incoming events are consumed once by Go, persisted, and then broadcast to browsers over SSE, so media and delivery state survive browser and container restarts.

## Preview the interface now

The UI does not require an RPC key or running bridge to render:

```sh
GOCACHE=/tmp/websideband-go-cache go run ./cmd/websideband
```

Open <http://127.0.0.1:8080>. Sending and network actions remain unavailable, but navigation, responsive layouts, browser-local contacts, and conversation rendering can be exercised.

Example message body:

```json
{
  "destination": "0123456789abcdef0123456789abcdef",
  "content": "Hello",
  "title": "",
  "method": "direct"
}
```

## Local validation

```sh
GOCACHE=/tmp/websideband-go-cache go test ./...
python3 -m py_compile services/lxmf-bridge/bridge.py
```

An end-to-end RF test requires the bridge to run in the existing Reticulum container's network namespace as described in the deployment guide.

The bridge also requires the same explicit `rpc_key` as `rnsd`. Official RNS uses authenticated control RPC during direct LXMF links, even though packet transport itself uses port `37428`.

## Next phases

1. Validate send, receive, announce, and delivery states against Sideband over both RNode RF and the configured TCP Reticulum interface.
2. Add Sideband-compatible generic file attachments, replies, reactions, and propagation controls.
3. Add authenticated HTTPS access for LAN/mobile deployment.
4. Expand known-destination, announce, telemetry, and map persistence and UI.
