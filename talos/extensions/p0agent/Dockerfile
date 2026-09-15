# Talos system-extension image: /manifest.yaml + /rootfs (overlaid on the
# Talos root filesystem by the installer). Build context = this directory
# with the static binary copied in as ./p0agent — see build.sh.
FROM scratch
COPY manifest.yaml /manifest.yaml
COPY rootfs/ /rootfs/
COPY p0agent /rootfs/usr/local/lib/containers/p0agent/p0agent
