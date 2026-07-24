#!/bin/sh
# Fly.io entrypoint: decrypt cluster secrets into tmpfs, then start the
# config server. Plaintext secrets only ever exist in /dev/shm (memory);
# the image and the VM disk hold only .age ciphertext.
set -eu

: "${AGE_KEY:?AGE_KEY secret not set (fly secrets set AGE_KEY=...)}"

mkdir -p /dev/shm/talos
cp -R /app/talos/. /dev/shm/talos/

keyfile=/dev/shm/age-key.txt
printf '%s\n' "$AGE_KEY" > "$keyfile"

# Fail loudly if any shipped .age file cannot be decrypted — a silently
# missing secrets patch would compose a broken machine config.
find /dev/shm/talos -name '*.age' | while IFS= read -r f; do
    out="${f%.age}"
    if ! age -d -i "$keyfile" -o "$out" "$f"; then
        echo "FATAL: cannot decrypt $f with the fly deploy key" >&2
        exit 1
    fi
    echo "decrypted ${out#/dev/shm/talos/}"
done

rm -f "$keyfile"

# WireGuard control channel: enabled when WG_ENDPOINT is set. Starts
# SEALED — no key material at rest; an admin unseals at runtime by
# signing the master message at /verify (wallet). WG_SERVER_PUBKEY
# pins the expected derived pubkey so a wrong-wallet unseal fails.
WG_ARGS=""
if [ -n "${WG_ENDPOINT:-}" ]; then
    WG_ARGS="--wg-port 51820 --wg-endpoint $WG_ENDPOINT ${WG_SERVER_PUBKEY:+--wg-server-pubkey $WG_SERVER_PUBKEY} --auto-bootstrap ${KMS_ADVERTISE:+--kms-advertise $KMS_ADVERTISE}"
fi

# shellcheck disable=SC2086
exec config-server \
    --root /dev/shm/talos \
    --bind 0.0.0.0 \
    --port 8080 \
    --require-auth \
    ${ADMIN_ADDRESSES:+--admin-address "$ADMIN_ADDRESSES"} \
    $WG_ARGS
