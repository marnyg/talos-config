#!/bin/sh
# Fly.io entrypoint: stage the talos tree into tmpfs and start the
# config server. The image and the VM disk hold only .age ciphertext;
# the server decrypts it into /dev/shm at UNSEAL time with the
# wallet-derived age identity (wgderive.AgeIdentity) — no AGE_KEY
# secret, and no plaintext anywhere until an admin signs.
set -eu

mkdir -p /dev/shm/talos
cp -R /app/talos/. /dev/shm/talos/

# WireGuard control channel: enabled when WG_ENDPOINT is set. Starts
# SEALED — no key material at rest; an admin unseals at runtime by
# signing the master message at /status (wallet). WG_SERVER_PUBKEY
# pins the expected derived pubkey so a wrong-wallet unseal fails.
WG_ARGS=""
if [ -n "${WG_ENDPOINT:-}" ]; then
    WG_ARGS="--wg-port 51820 --wg-endpoint $WG_ENDPOINT ${WG_SERVER_PUBKEY:+--wg-server-pubkey $WG_SERVER_PUBKEY} --auto-bootstrap ${KMS_ADVERTISE:+--kms-advertise $KMS_ADVERTISE} ${WG_ADMIN_PEERS:+--wg-admin-peers $WG_ADMIN_PEERS}"
fi

# Nebula mesh: enabled when MESH_ENDPOINT is set. No second secret and
# no second unseal — the mesh derives from the same master as wg0, which
# is also why --mesh-port is only accepted alongside --wg-port.
MESH_ARGS=""
if [ -n "${MESH_ENDPOINT:-}" ]; then
    MESH_ARGS="--mesh-port 4242 --mesh-endpoint $MESH_ENDPOINT ${MESH_DEVICES:+--mesh-devices $MESH_DEVICES} ${MESH_MEDIA_DEVICES:+--mesh-media-devices $MESH_MEDIA_DEVICES}"
fi

# shellcheck disable=SC2086
exec config-server \
    --root /dev/shm/talos \
    --bind 0.0.0.0 \
    --port 8080 \
    --require-auth \
    ${ADMIN_ADDRESSES:+--admin-address "$ADMIN_ADDRESSES"} \
    $WG_ARGS \
    $MESH_ARGS
