# homescreen

An iOS home-screen style dashboard for your NAS. It has squircle app icons, swipeable pages, a frosted dock, a big clock and gradient or photo wallpapers. Red badges show which services are down.

- **One ~10 MB container.** It's a static Go binary on `scratch` and makes no outside network calls.
- **Configured with YAML.** The file is re-read on every page load, so you just edit and refresh.
- **Icons by naming convention.** An app called `Radarr` uses the `radarr` icon automatically.
- **Built-in icons** for Radarr, Sonarr, Lidarr, Prowlarr, SABnzbd, Audiobookshelf and Calibre. Any other app gets a generated letter icon.
- **Can be added to your iPhone home screen.** In Safari, use Share → Add to Home Screen and it opens fullscreen like an app.

## Quick start

```sh
mkdir -p config/icons
cp config.example.yaml config/config.yaml   # then edit it
docker compose up -d --build
```

Open `http://<nas>:8090`.

## Deploying to the NAS

Most NAS boxes (Synology, Unraid, TrueNAS, QNAP) are **amd64**. If you build on an Apple Silicon Mac you need to target that platform, or the container fails with `exec format error`.

**Option A: build on the NAS.** Copy this folder over and run `docker compose up -d --build` there.

**Option B: build on your Mac and ship the image over SSH.**

```sh
docker buildx build --platform linux/amd64 -t homescreen:latest --load .
docker save homescreen:latest | ssh user@nas sudo docker load
```

Then on the NAS, use the compose file without `build:` (or run it directly):

```sh
docker run -d --name homescreen --restart unless-stopped \
  -p 8090:8080 -v /volume1/docker/homescreen:/config:ro homescreen:latest
```

**Option C: pull the published image.** The GitHub workflow (see below) publishes a multi-arch image (amd64 + arm64) to `ghcr.io/danielgratzl/homescreen`. In `docker-compose.yml`, replace the `build:` and `image:` lines with:

```yaml
    image: ghcr.io/danielgratzl/homescreen:latest
```

To update: `docker compose pull && docker compose up -d`.

If the pull is denied, the package is private. Either make it public (GitHub → your profile → Packages → homescreen → Package settings → Change visibility), or log in on the NAS with a personal access token that has `read:packages`:

```sh
echo <token> | sudo docker login ghcr.io -u danielgratzl --password-stdin
```

## CI/CD (GitHub Actions)

[`.github/workflows/docker.yml`](.github/workflows/docker.yml) runs `gofmt`, `go vet` and a build, then publishes to GitHub Container Registry:

| Trigger                | Image tags                       |
|------------------------|----------------------------------|
| Pull request           | none (test build only, amd64)    |
| Push to `main`         | `latest`, `<short-sha>`          |
| Git tag `v1.2.3`       | `1.2.3`, `1.2`, `<short-sha>`    |

To release a version: `git tag v1.0.0 && git push --tags`. You can also start a run by hand from the Actions tab.

Notes:
- arm64 is built with QEMU emulation on GitHub's amd64 runners, which adds a minute or two.
- Layers are cached in the GitHub Actions cache.
- Publishing uses the built-in `GITHUB_TOKEN`, so no secrets need to be set up.

The container runs as UID `65532`. The mounted config folder only needs to be world-readable, which is the default for most files. If your NAS uses stricter permissions, add `user: "1026:100"` (your PUID:PGID) to the compose service.

## Configuration

The full annotated example is in [`config.example.yaml`](config.example.yaml).

```yaml
title: NAS
wallpaper: aurora          # aurora | sunset | ocean | forest | graphite | dusk | my-photo.jpg | https://…
clock: true
new_tab: true
columns: 8                 # icons per row on wide screens (phones: 4, tablets: 6)
status_checks: true
check_interval: 30         # seconds

pages:                     # swipe, use ← →, or scroll the mouse wheel to change pages
  - name: Media
    apps:
      - name: Radarr
        url: http://nas.local:7878
      - name: Calibre Web               # → looks for calibre-web, then its alias calibre
        url: http://nas.local:8083
      - name: Plex
        url: http://nas.local:32400/web
        icon: https://example.com/plex.png   # explicit URL…
      - name: Home Assistant
        url: http://nas.local:8123
        icon: hass                            # …or a different slug
        check_url: http://homeassistant:8123  # what the container pings
        check: false                          # turn the status badge off
        new_tab: false

dock:                      # frosted bar at the bottom; labels are hidden like on iOS
  - name: Audiobookshelf
    url: http://nas.local:13378
```

If you only need one page, you can use a top-level `apps:` list instead of `pages:`.

Unknown keys are rejected with a readable error banner, so typos like `wallpapper:` don't fail silently. If no config file is mounted, the sample config is shown.

### Wallpaper

Use one of the presets, or put an image (`.jpg`, `.png`, `.webp`, `.avif`, `.gif`) **next to `config.yaml`** and reference it by filename, e.g. `wallpaper: mountains.jpg`.

## Icons

Each app's name is turned into a slug: lowercase, with spaces, `_` and `.` replaced by `-`, and other characters dropped.

| App name          | Slug             |
|-------------------|------------------|
| `Radarr`          | `radarr`         |
| `Calibre Web`     | `calibre-web`    |
| `SABnzbd`         | `sabnzbd`        |
| `Home Assistant`  | `home-assistant` |

The first match wins:

1. `/config/icons/<slug>.svg|png|webp|jpg|jpeg`: your own icons, which override the built-in ones
2. a built-in icon (`icons/<slug>.svg` in this repo)
3. a generated letter icon, with a color derived from the name

To use a different slug, set `icon: <slug>`, or give a full URL with `icon: https://…`.

Built-in aliases: `calibre-web` → `calibre`, `abs` → `audiobookshelf`, `sab` → `sabnzbd`, `liddar` → `lidarr`.

**Icon tips**

- Use square images, at least 180×180. They are clipped to the iOS squircle shape, so use full-bleed artwork without your own rounded corners.
- For the official logos of hundreds of self-hosted apps, the community [dashboard-icons](https://github.com/homarr-labs/dashboard-icons) set works well. Download `radarr.png` and drop it into `config/icons/` to replace the built-in icon. Note that those logos are transparent, so they look best on a solid background.
- Icons are cached by the browser for 5 minutes.

## Status checks

The server sends a GET request to each app's `check_url`, or to its `url` if `check_url` isn't set. Any HTTP response counts as up, including 401/403 from apps that require a login. Timeouts (4 s), connection errors and 502/503/504 (a reverse proxy whose backend is stopped) count as down, and those apps get a red **!** badge. Self-signed TLS certificates are accepted.

The check runs **from inside the container**, so `nas.local` or `localhost` may not resolve there. Use `check_url` with the container name (when the apps share a Docker network) or the NAS's LAN IP.

## Development

```sh
CONFIG_PATH=./config/config.yaml PORT=8090 go run .
```

Layout: `main.go` (server), `web/` (HTML/CSS/JS, embedded), and `icons/` (built-in SVGs, embedded).
