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
# config-server/go.mod `replace`s ../protocol (the Issuer actor, 359.8.1),
# so the protocol module must sit beside it at the same relative path.
WORKDIR /src/config-server
COPY protocol/go.mod protocol/go.sum /src/protocol/
COPY config-server/go.mod config-server/go.sum ./
RUN go mod download
COPY protocol/ /src/protocol/
COPY config-server/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/config-server .

# iroh home relay (Mesh v3, ADR-0022): the upstream image ships a static
# musl binary at /iroh-relay — no build step. The tag must match the
# core version iroh-go pins (iroh-go/nix/sources.nix, 1.1.0); bump both
# together. config-server runs it as a child and proxies /relay on 8080.
FROM n0computer/iroh-relay:v1.1.0 AS relay

FROM alpine:3.21
COPY --from=build /out/config-server /usr/local/bin/config-server
COPY --from=relay /iroh-relay /usr/local/bin/iroh-relay
COPY talos /app/talos
COPY fly/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
