# Podman proof-of-concept deployment

This deployment keeps three independent containers. The `reticulum` container remains the only process with RNode USB access. The LXMF bridge shares its network namespace solely because official RNS shared-instance TCP is loopback-only.

The repository includes `services/reticulum/Containerfile` for a new local gateway. Skip that image when attaching PEREVIA to an existing gateway.

## Preconditions

- The existing container is named `reticulum`.
- Its RNS configuration has `share_instance = Yes`, `shared_instance_type = tcp`, `shared_instance_port = 37428`, and `instance_control_port = 37429`.
- Its `[reticulum]` section contains an explicit random `rpc_key`. The bridge must receive the same value.
- Port `8081` is unused inside the Reticulum container's network namespace.
- No host publishing exists for ports `37428`, `37429`, or `8081`.

## Build

From the repository root:

```sh
podman build -t localhost/websideband:dev -f Containerfile .
podman build -t localhost/lxmf-bridge:dev -f services/lxmf-bridge/Containerfile services/lxmf-bridge
podman build -t localhost/websideband-reticulum:dev -f services/reticulum/Containerfile services/reticulum
```

## Private network

Create a private bridge and attach the existing Reticulum container. This does not move USB ownership or start another RNS stack.

```sh
podman network create websideband-internal
podman network connect websideband-internal reticulum
```

Generate a bridge API token and a separate shared-instance RPC key. Retain both in your secret manager:

```sh
openssl rand -hex 32
openssl rand -hex 32
```

Add the second value to the existing `rnsd` configuration and restart that container:

```ini
[reticulum]
  rpc_key = RPC_KEY
```

Create the identity/state volume:

```sh
podman volume create lxmf-bridge-data
podman volume create websideband-data
```

## Start the LXMF bridge

Replace `TOKEN` with the bridge API token and `RPC_KEY` with the value configured in `rnsd`.

```sh
podman run -d \
  --name lxmf-bridge \
  --network container:reticulum \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --cap-drop all \
  --security-opt no-new-privileges \
  --volume lxmf-bridge-data:/data:Z,U \
  --env LXMF_BRIDGE_TOKEN=TOKEN \
  --env LXMF_DISPLAY_NAME='PEREVIA' \
  --env RNS_SHARED_INSTANCE_PORT=37428 \
  --env RNS_INSTANCE_CONTROL_PORT=37429 \
  --env RNS_RPC_KEY=RPC_KEY \
  localhost/lxmf-bridge:dev
```

`--network container:reticulum` makes `127.0.0.1:37428` refer to the existing `rnsd`. The bridge's HTTP listener on `0.0.0.0:8081` is reachable only through networks already attached to `reticulum`; it is not published to the host.

## Start the Go service

```sh
podman run -d \
  --name websideband \
  --network websideband-internal \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --cap-drop all \
  --security-opt no-new-privileges \
  --publish 8080:8080 \
  --volume websideband-data:/data:Z,U \
  --env WEBSIDEBAND_DATA_DIR=/data \
  --env LXMF_BRIDGE_URL=http://reticulum:8081 \
  --env LXMF_BRIDGE_TOKEN=TOKEN \
  localhost/websideband:dev
```

Only `8080` is published. Bind it to a specific LAN address if desired, for example `--publish 192.168.1.10:8080:8080`.

Authentication is enabled by default. On the first visit, create the administrator username and a password of at least 12 characters. The bcrypt password hash is stored in the private `websideband-data` volume; the plaintext password is never stored.

### HTTPS for phones and LAN access

Camera, microphone, PWA, and notification features require a trusted HTTPS origin on phones. Obtain a certificate whose subject names include the hostname or LAN address used by clients, mount it read-only, and start the Go service on a TLS port:

```sh
podman run -d \
  --name websideband \
  --network websideband-internal \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=16m \
  --cap-drop all \
  --security-opt no-new-privileges \
  --publish 8443:8443 \
  --volume websideband-data:/data:Z,U \
  --volume ./tls:/tls:ro,Z \
  --env WEBSIDEBAND_LISTEN_ADDRESS=:8443 \
  --env WEBSIDEBAND_TLS_CERT_FILE=/tls/websideband.crt \
  --env WEBSIDEBAND_TLS_KEY_FILE=/tls/websideband.key \
  --env WEBSIDEBAND_DATA_DIR=/data \
  --env LXMF_BRIDGE_URL=http://reticulum:8081 \
  --env LXMF_BRIDGE_TOKEN=TOKEN \
  localhost/websideband:dev
```

Open `https://HOSTNAME:8443`. A self-signed certificate only works after its issuing CA is installed and trusted on every phone; do not bypass certificate warnings for routine use. Keep plain HTTP bound to `127.0.0.1` only.

## Verify

```sh
curl --fail http://127.0.0.1:8080/healthz
curl --fail http://127.0.0.1:8080/api/v1/status
curl -N http://127.0.0.1:8080/api/v1/events
```

Trigger an announce:

```sh
curl --fail \
  --request POST \
  http://127.0.0.1:8080/api/v1/announce
```

Send a direct text message after replacing the destination:

```sh
curl --fail \
  --request POST \
  --header 'Content-Type: application/json' \
  --data '{"destination":"0123456789abcdef0123456789abcdef","content":"Hello from PEREVIA","method":"direct"}' \
  http://127.0.0.1:8080/api/v1/messages
```

The send request returns after the message enters the LXMF router. Follow `/api/v1/events` for delivery or failure callbacks.

## Persistent data

The `lxmf-bridge-data` volume contains:

- `/data/identity`: the private Reticulum identity, mode `0600`;
- `/data/rns`: the bridge client's RNS cache and generated shared-instance configuration;
- `/data/lxmf`: LXMRouter state, tickets, ratchets, and queues.

Back up this volume securely. Losing the identity changes the LXMF address. Anyone obtaining it can impersonate and decrypt traffic for that identity.

## Control port note

Reticulum packets use the shared-instance data plane on `37428`, but the official client also queries authenticated RPC on `37429` while establishing and operating direct links. Without a matching explicit `rpc_key`, opportunistic operations may appear to work while direct send/receive fails with `digest sent was rejected`. Keep both ports loopback-only; sharing the network namespace provides access without publishing either port.
