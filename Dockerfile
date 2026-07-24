# Config server for fly.io. Plaintext secrets are excluded via
# .dockerignore; only .age ciphertext ships in the image and is
# decrypted into tmpfs at startup by fly/entrypoint.sh.
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY config-server/go.mod config-server/go.sum ./
RUN go mod download
COPY config-server/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/config-server .

FROM alpine:3.21
RUN apk add --no-cache age
COPY --from=build /out/config-server /usr/local/bin/config-server
COPY talos /app/talos
COPY fly/entrypoint.sh /usr/local/bin/entrypoint.sh
RUN chmod +x /usr/local/bin/entrypoint.sh
ENTRYPOINT ["/usr/local/bin/entrypoint.sh"]
