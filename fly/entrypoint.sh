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

# shellcheck disable=SC2086
exec config-server \
    --root /dev/shm/talos \
    --bind 0.0.0.0 \
    --port 8080 \
    --require-auth \
    ${ADMIN_ADDRESSES:+--admin-address "$ADMIN_ADDRESSES"} \
    $WG_ARGS
