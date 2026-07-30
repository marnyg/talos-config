#!/bin/sh
# Fly.io entrypoint: stage the talos tree into tmpfs and start the
# config server. The image and the VM disk hold only .age ciphertext;
# the server decrypts it into /dev/shm at UNSEAL time with the
# wallet-derived age identity (masterderive.AgeIdentity) — no AGE_KEY
# secret, and no plaintext anywhere until an admin signs.
set -eu

mkdir -p /dev/shm/talos
cp -R /app/talos/. /dev/shm/talos/

# Nebula mesh: enabled when MESH_ENDPOINT is set. Starts SEALED — no
# key material at rest; an admin unseals at runtime by signing the
# master message at /status (wallet). MESH_CA_PIN pins the expected
# derived CA fingerprint so a wrong-wallet unseal fails loudly.
MESH_ARGS=""
if [ -n "${MESH_ENDPOINT:-}" ]; then
    MESH_ARGS="--mesh-port 4242 --mesh-endpoint $MESH_ENDPOINT ${MESH_CA_PIN:+--mesh-ca-pin $MESH_CA_PIN} ${MESH_DEVICES:+--mesh-devices $MESH_DEVICES} ${MESH_MEDIA_DEVICES:+--mesh-media-devices $MESH_MEDIA_DEVICES} --auto-bootstrap ${KMS_ADVERTISE:+--kms-advertise $KMS_ADVERTISE}"
fi

# shellcheck disable=SC2086
exec config-server \
    --root /dev/shm/talos \
    --bind 0.0.0.0 \
    --port 8080 \
    --require-auth \
    ${ADMIN_ADDRESSES:+--admin-address "$ADMIN_ADDRESSES"} \
    $MESH_ARGS
