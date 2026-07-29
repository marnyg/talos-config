# Config server for fly.io. Plaintext secrets are excluded via
# .dockerignore; only .age ciphertext ships in the image and is
# decrypted into tmpfs at unseal time by the server itself (wallet-
# derived age identity — no key material in the image or in secrets).
# Toolchain tag must satisfy the `go` directive in config-server/go.mod —
# the official images pin GOTOOLCHAIN=local, so an older tag fails at
# `go mod download` instead of silently downloading a newer toolchain.
# The nix build pins the same version separately (buildGo126Module in
# flake.nix); both follow go.mod, so bump all three together.
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY config-server/go.mod config-server/go.sum ./
RUN go mod download
COPY config-server/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/config-server .

FROM alpine:3.21
COPY --from=build /out/config-server /usr/local/bin/config-server
COPY talos /app/talos
COPY fly/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
