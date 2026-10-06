# Static, FROM-scratch image for one C-free config-server/cmd binary —
# the in-cluster Go services: the SIWE→OIDC bridge (cmd/siweoidc) and
# the Jellyfin front proxy (cmd/jellyfinqc). Built and pushed to ghcr
# by .github/workflows/<name>-image.yml with `--build-arg CMD=<name>`.
# These services hold no secrets of their own beyond what the env
# hands them, read no files and need no shell, so the image is the
# binary and nothing else. Toolchain tag follows go.mod — bump
# together with the root Dockerfile and flake.nix (see the note there).
#
# Layout mirrors the repo because config-server/go.mod replaces its
# sibling modules by relative path (../protocol, ../iroh-transport,
# ../iroh-go). Both binaries compile protocol/cert; the iroh modules
# are only needed as go.mod files so the module graph loads — nothing
# from them is built, and the binary stays CGO-free. The .dockerignore
# allowlist keeps the context to exactly these paths.
ARG CMD
FROM golang:1.26-alpine AS build
ARG CMD
WORKDIR /src
COPY protocol/ protocol/
COPY iroh-transport/go.mod iroh-transport/go.sum iroh-transport/
COPY iroh-go/go.mod iroh-go/
COPY config-server/ config-server/
WORKDIR /src/config-server
RUN test -n "$CMD" && CGO_ENABLED=0 go build -trimpath -o /out/app ./cmd/$CMD

FROM scratch
COPY --from=build /out/app /app
USER 65534:65534
ENTRYPOINT ["/app"]
