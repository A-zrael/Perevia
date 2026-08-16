#!/bin/sh
set -eu

: "${RNS_RPC_KEY:?RNS_RPC_KEY must be set}"
RNS_RPC_KEY="$(printf %s "$RNS_RPC_KEY" | tr -d '[:space:]')"

case "$RNS_RPC_KEY" in
  *[!0-9a-fA-F]*|'') echo "RNS_RPC_KEY must contain only hexadecimal characters" >&2; exit 1 ;;
esac

shared_port="${RNS_SHARED_INSTANCE_PORT:-37428}"
control_port="${RNS_INSTANCE_CONTROL_PORT:-37429}"

sed \
  -e "s/__SHARED_PORT__/$shared_port/g" \
  -e "s/__CONTROL_PORT__/$control_port/g" \
  -e "s/__RPC_KEY__/$RNS_RPC_KEY/g" \
  /opt/reticulum/config.template > /data/config
chmod 0600 /data/config

exec rnsd --config /data
