#!/bin/sh
# Fly.io entrypoint: stage the talos tree into tmpfs and start the
# config server. The image and the VM disk hold only .age ciphertext;
# the server decrypts it into /dev/shm at UNSEAL time with the
# wallet-derived age identity (masterderive.AgeIdentity) — no AGE_KEY
# secret, and no plaintext anywhere until an admin signs.
set -eu

mkdir -p /dev/shm/talos
cp -R /app/talos/. /dev/shm/talos/

# iroh home relay (Mesh v3, ADR-0022): a keyless child of the hub,
# plain HTTP on loopback, proxied on :8080 so 443 stays the single
# entrypoint. Runs sealed or not. Set RELAY_DISABLE=1 to leave it off.
RELAY_ARGS=""
if [ -z "${RELAY_DISABLE:-}" ] && [ -x /usr/local/bin/iroh-relay ]; then
    RELAY_ARGS="--relay-bin /usr/local/bin/iroh-relay"
fi

# The hub proper (ADR-0024): --iroh-relay makes this process a hub —
# the hubkey is the EndpointId, homed on the relay child above and
# advertised to members as IROH_RELAY_URL (the public hostname). It
# starts SEALED — no key material at rest; an admin unseals at runtime
# by signing the master message and the speak-as proposal at /status
# (wallet). Unset = a plain config server: no identity plane, no KMS,
# no auto-bootstrap.
HUB_ARGS=""
if [ -n "${IROH_RELAY_URL:-}" ]; then
    HUB_ARGS="--iroh-relay $IROH_RELAY_URL --auto-bootstrap ${KMS_ADVERTISE:+--kms-advertise $KMS_ADVERTISE}"
fi

# shellcheck disable=SC2086
exec config-server \
    --root /dev/shm/talos \
    --bind 0.0.0.0 \
    --port 8080 \
    --require-auth \
    $RELAY_ARGS \
    $HUB_ARGS \
    ${ADMIN_ADDRESSES:+--admin-address "$ADMIN_ADDRESSES"}
