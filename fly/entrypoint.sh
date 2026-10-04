#!/bin/sh
# Fly.io entrypoint: stage the talos tree into tmpfs and start the
# config server. The image and the VM disk hold only .age ciphertext;
# the server decrypts it into /dev/shm at UNSEAL time with the
# wallet-derived age identity (masterderive.AgeIdentity) — no AGE_KEY
# secret, and no plaintext anywhere until an admin signs.
#
# The served tree is a SYMLINK (talos-config-k9h7): it starts on the
# baked copy and, when GIT_REMOTE is set, the hub re-points it at the
# verified tip of GIT_REF (default main) — a tip ssh-signed by a key in
# the baked talos/allowed-signers. The baked copy is the fallback: a
# fetch or verification failure leaves the last-good tree in place.
# Fetched trees live beside it in /dev/shm/hub, tmpfs like everything
# else here. Unset GIT_REMOTE to serve the baked tree only.
set -eu

mkdir -p /dev/shm/hub/talos-baked
cp -R /app/talos/. /dev/shm/hub/talos-baked/
ln -sfn /dev/shm/hub/talos-baked /dev/shm/hub/talos

GIT_ARGS=""
if [ -n "${GIT_REMOTE:-}" ]; then
    GIT_ARGS="--git-remote $GIT_REMOTE --git-ref ${GIT_REF:-main} ${GIT_POLL:+--git-poll $GIT_POLL}"
fi

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
    --root /dev/shm/hub/talos \
    --bind 0.0.0.0 \
    --port 8080 \
    --require-auth \
    $RELAY_ARGS \
    $GIT_ARGS \
    $HUB_ARGS \
    ${ADMIN_ADDRESSES:+--admin-address "$ADMIN_ADDRESSES"}
