# Talos system-extension image: /manifest.yaml + /rootfs (overlaid on the
# Talos root filesystem by the installer). Build context = this directory
# with the static binary copied in as ./nodeagent — see build.sh.
FROM scratch
COPY manifest.yaml /manifest.yaml
COPY rootfs/ /rootfs/
COPY nodeagent /rootfs/usr/local/lib/containers/p0agent/nodeagent
