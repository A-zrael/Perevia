# PEREVIA feature-parity roadmap

## Compatibility target

PEREVIA should use the official LXMF field structures rather than inventing attachment formats:

- images: `FIELD_IMAGE` as `[extension, bytes]`;
- files: `FIELD_FILE_ATTACHMENTS` as a list of `[filename, bytes]` entries;
- audio messages: `FIELD_AUDIO` as `[audio_mode, bytes]`;
- formatting: `FIELD_RENDERER`;
- replies and reactions: `FIELD_REPLY_TO`, `FIELD_REPLY_QUOTE`, and `FIELD_REACTION`;
- telemetry and commands: `FIELD_TELEMETRY`, `FIELD_TELEMETRY_STREAM`, `FIELD_COMMANDS`, and `FIELD_RESULTS`.

These structures are implemented by official LXMF 1.1.0 and used by current Sideband. The Python bridge remains responsible only for translating these fields to and from typed bridge events. It must not own contacts, conversations, attachment policy, or UI state.

## Storage design

SQLite stores identities, contacts, conversations, messages, delivery attempts, known destinations, announces, attachment metadata, settings, and event checkpoints. Binary attachment data is stored in a private application data directory and referenced from SQLite by an opaque ID and content hash.

Browser uploads go to the Go application using bounded multipart requests. Go validates names, declared and detected media types, and configured size limits before saving the data. The bridge receives attachment bytes only when creating an outbound LXMF message. Incoming bridge events transfer attachment data to Go, which stores it before broadcasting a metadata-only realtime event to browsers.

Downloads use opaque attachment IDs, `Content-Disposition: attachment`, `X-Content-Type-Options: nosniff`, and authorization checks. Received files are never executed or rendered as active HTML. Images use a restricted allowlist and are served from a non-scriptable endpoint.

## Delivery order

1. **Persistence foundation**: SQLite repositories, migrations, durable messages, contacts, announces, and delivery-state updates.
2. **Images and files**: interoperable send/receive, upload progress, previews, downloads, configurable limits, and image compression.
3. **Message semantics**: replies, quotes, reactions, Markdown indication, cancellation, retry, and propagation fallback/sync.
4. **Voice notes**: browser recording, interoperable Opus/Ogg or supported Codec2 field modes, playback, and durable storage.
5. **Network detail**: path requests, hop counts, link statistics, propagation-node selection, sync progress, tickets, stamps, and QR/paper messages.
6. **Telemetry and maps**: authenticated telemetry exchange, sensor history, location consent, and offline-capable map layers.
7. **Live voice**: a separate LXST integration component; calls are not ordinary LXMF attachment messages.
8. **Plugins and commands**: disabled by default, explicitly trusted per contact, allowlisted, auditable, and isolated from the web and Reticulum containers.

## Initial limits

- Keep Sideband's approximately 1 MB compatibility ceiling configurable, but use a smaller default suitable for LoRa.
- Reject oversized uploads before loading them fully into memory.
- Compress and resize images in the browser, while preserving an option to send the original as a file.
- Show estimated payload size and warn that large transfers can take substantial time over RNode links.

## Service boundaries

- `reticulum`: physical interfaces, transport, routing, shared instance, and no public ports.
- `lxmf-bridge`: identity, LXMF/RNS field encoding, delivery callbacks, propagation operations, and no application database.
- `websideband`: authentication, SQLite, contacts, conversations, files, HTTP API, realtime events, and browser UI.
- future `lxst-bridge`: optional real-time voice integration, independently deployable.
