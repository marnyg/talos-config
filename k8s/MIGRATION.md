# Media Stack Migration Plan

Migrate Docker Compose media stack to Kubernetes (ArgoCD-managed).
Services are **stateless** — no persistent config volumes. External secrets via Sealed Secrets, media on PVs.

## Services

### Jellyfin (media streaming)
- **Image**: `lscr.io/linuxserver/jellyfin:10.11.6`
- **Ports**: 8096 (NodePort)
- **Volumes**: `/data/tv` (RO), `/data/movies` (RO)
- **Config**: Stateless — discovers libraries on startup

### Sonarr (TV management)
- **Image**: `ghcr.io/linuxserver/sonarr:4.0.17`
- **Ports**: 8989 (NodePort)
- **Volumes**: `/tv` (RW), `/downloads` (RW)
- **Init**: Configure via Sonarr API after startup:
  - Download client: NZBget at `http://nzbget:6789`
  - Download client: Transmission at `http://transmission:9091`
  - Indexer: Jackett at `http://jackett:9117`
  - Root folder: `/tv`

### Radarr (movie management)
- **Image**: `lscr.io/linuxserver/radarr:6.0.4`
- **Ports**: 7878 (NodePort)
- **Volumes**: `/movies` (RW), `/downloads` (RW)
- **Init**: Same as Sonarr but root folder `/movies`

### NZBget (usenet downloader)
- **Image**: `ghcr.io/linuxserver/nzbget:24.12`
- **Ports**: 6789 (ClusterIP — internal only)
- **Volumes**: `/downloads` (RW)
- **Secrets**: Newshosting NNTP credentials (via Sealed Secret)
- **Config**: Mount nzbget.conf ConfigMap with NNTP settings from secret

### Transmission (torrent client)
- **Image**: `ghcr.io/linuxserver/transmission:4.1.1`
- **Ports**: 9091 (ClusterIP), 51413 TCP+UDP (NodePort — peer port)
- **Volumes**: `/downloads` (RW)
- **Config**: Mount settings.json ConfigMap

### Jackett (indexer aggregator)
- **Image**: `ghcr.io/linuxserver/jackett:0.24.1`
- **Ports**: 9117 (ClusterIP — internal only)
- **Config**: Stateless — nyaasi indexer configured on startup

## Storage

### Persistent Volumes (hostPath on NVMe)
- `media-tv` — TV shows, mounted as `/tv`
- `media-movies` — Movies, mounted as `/movies`
- `media-downloads` — Active downloads, mounted as `/downloads`

## Secrets

Only external credential that cannot be regenerated:

| Secret | Value |
|--------|-------|
| Newshosting NNTP host | `news.newshosting.com` |
| Newshosting NNTP port | `119` |
| Newshosting NNTP user | `marnyg31` |
| Newshosting NNTP pass | `fVT85E4SaDqvjb` |

All service API keys (Sonarr, Radarr, Jackett, NZBget control) are auto-generated on first startup. Init containers read the generated keys to wire up service connections.

Secret management via **Sealed Secrets**: encrypted SealedSecret resource committed to git, decrypted in-cluster.

## Init Automation (stateless service wiring)

Since all API keys are generated fresh, init containers follow this pattern:

1. Wait for target service to be ready
2. Read its auto-generated API key (from its API or config)
3. Configure connections (download clients, indexers, root folders)

```
Startup order:
  Jackett, NZBget, Transmission  (no deps, start in parallel)
           ↓ ready
  Sonarr init: GET jackett API key, configure indexer
               configure NZBget + Transmission as download clients
               set root folder /tv
  Radarr init: same, root folder /movies
           ↓ ready
  Jellyfin  (just needs media PVs)
```

## Service Dependency Graph

```
Jackett ← Sonarr → NZBget     ↘
                 ↘ Transmission → /downloads → /tv    → Jellyfin
Jackett ← Radarr → NZBget     ↗                /movies ↗
                 ↘ Transmission
```

## Migration Order

1. Install Sealed Secrets controller (via ArgoCD/extraManifests)
2. Create and apply SealedSecret for Newshosting credentials
3. Create PVs (hostPath on NVMe)
4. Deploy Jackett + NZBget + Transmission (no deps)
5. Deploy Sonarr + Radarr (init containers wire up connections)
6. Deploy Jellyfin (read-only media access)

## Repo Structure

```
k8s/apps/
  storage/          # PVs and PVCs
  jackett/          # Deployment + Service
  nzbget/           # Deployment + Service + ConfigMap
  transmission/     # Deployment + Service + ConfigMap
  sonarr/           # Deployment + Service + init container
  radarr/           # Deployment + Service + init container
  jellyfin/         # Deployment + Service
  sealed-secrets/   # SealedSecret resources (safe to commit)
```
