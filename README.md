# Seedly

Open-source seedbox: Go BitTorrent engine ([`anacrolix/torrent`](https://github.com/anacrolix/torrent)), SQLite, and a Vite React UI.

## Legal notice

Seedly is intended **only** for downloading and seeding content that you have the legal right to distribute (for example public-domain, Creative Commons, or other licensed material).

You are solely responsible for how you use this software and for complying with the laws that apply to you. The authors and contributors accept **no responsibility or liability** for misuse, including use with unauthorized or illegal torrents.

## Quick start

### Docker image (GHCR)

CI builds and publishes `ghcr.io/lerenn/seedly` on pushes to `main` and on version tags (`v*`).

```bash
docker pull ghcr.io/lerenn/seedly:latest
docker run --rm -p 8080:8080 \
  -e SEEDLY_ADMIN_PASSWORD=changeme \
  -v seedly-db:/data/db \
  -v seedly-meta:/data/meta \
  -v seedly-data:/data/downloads \
  ghcr.io/lerenn/seedly:latest
```

If the package is private, authenticate first:

```bash
echo "$GITHUB_TOKEN" | docker login ghcr.io -u USERNAME --password-stdin
```

### Local compose build

```bash
export SEEDLY_ADMIN_PASSWORD=changeme
docker compose up --build
```

Open http://localhost:8080 and sign in with:

- username: `admin` (or `SEEDLY_ADMIN_USERNAME`)
- password: value of `SEEDLY_ADMIN_PASSWORD`

## Volumes

| Volume | Mount | Contents |
|--------|--------|----------|
| `seedly-db` | `/data/db` | SQLite database |
| `seedly-meta` | `/data/meta` | Uploaded `.torrent` files |
| `seedly-data` | `/data/downloads` | Downloaded / seeded data |

## Features

- Local username/password sessions; first admin bootstrapped from env when the DB is empty
- Users see only their torrents; admins can switch **View as** another user
- Access your torrent files over FTPS and from the file list in a torrent's detail panel
- Add torrents via `.torrent` upload, one or several files at a time (per-file errors are reported without blocking the rest)
- Progress / status / peers / downloaded / uploaded with pause, resume, delete
- Completed torrents keep seeding until paused or deleted
- Download completed content from the UI (single file, or streamed zip for multi-file)

## Local development

```bash
mkdir -p .data/{db,meta,downloads}
export SEEDLY_ADMIN_PASSWORD=changeme
export SEEDLY_DB_PATH=.data/db/seedly.db
export SEEDLY_META_PATH=.data/meta
export SEEDLY_DOWNLOADS_PATH=.data/downloads
export SEEDLY_WEB_PATH=web/dist
go run ./cmd/seedly

# UI (optional separate terminal for HMR)
cd web && npm install && npm run dev
```

Vite proxies `/api` to `:8080`.

## Legal test torrent

Use any small Creative Commons / public-domain `.torrent`, for example:

- https://webtorrent.io/torrents/sintel.torrent (CC-BY, Blender Foundation)

For fully offline local checks, `cmd/mktorrent` can build a tiny torrent with an HTTP webseed:

```bash
mkdir -p .testdata
printf 'hello\n' > .testdata/hello.txt
go run ./cmd/mktorrent \
  -data .testdata/hello.txt \
  -out .testdata/hello.torrent \
  -webseed http://127.0.0.1:9090/hello.txt \
  -serve :9090
```

Then upload `.testdata/hello.torrent` in the UI while the webseed server is running.

## Environment

| Variable | Default | Description |
|----------|---------|-------------|
| `SEEDLY_ADMIN_USERNAME` | `admin` | Bootstrap admin username |
| `SEEDLY_ADMIN_PASSWORD` | *(required)* | Bootstrap admin password |
| `SEEDLY_LISTEN` | `:8080` | HTTP listen address |
| `SEEDLY_DB_PATH` | `/data/db/seedly.db` | SQLite path |
| `SEEDLY_META_PATH` | `/data/meta` | `.torrent` storage |
| `SEEDLY_DOWNLOADS_PATH` | `/data/downloads` | Torrent data |
| `SEEDLY_WEB_PATH` | `web/dist` | Built SPA directory |
| `SEEDLY_SESSION_TTL` | `168h` | Session lifetime |
| `SEEDLY_COOKIE_SECURE` | `false` | Set `true` behind HTTPS |
| `SEEDLY_LOGIN_MAX_FAILURES` | `5` | Failed logins (web and FTP) before an IP is banned; `0` disables |
| `SEEDLY_LOGIN_BAN` | `15m` | Ban duration and failure-counting window |
| `SEEDLY_FTP_ENABLED` | `false` | Enable the FTP/FTPS server |
| `SEEDLY_FTP_LISTEN` | `:2121` | FTP control listen address |
| `SEEDLY_FTP_PUBLIC_HOST` | *(empty)* | IP or hostname announced in passive replies |
| `SEEDLY_FTP_PASV_PORTS` | `30000-30009` | Passive data port range (publish the same ports) |
| `SEEDLY_FTP_ALLOW_PLAINTEXT` | `false` | Allow FTP without TLS (not recommended) |
| `SEEDLY_FTP_TLS_CERT_FILE` / `SEEDLY_FTP_TLS_KEY_FILE` | *(empty)* | Existing certificate files (reloaded on change) |
| `SEEDLY_FTP_TLS_ACME_DOMAIN` | *(empty)* | Obtain/renew a Let's Encrypt cert for this domain (DNS-01) |
| `SEEDLY_FTP_TLS_ACME_DNS_PROVIDER` | *(empty)* | [lego DNS provider code](https://go-acme.github.io/lego/dns/) (e.g. `cloudflare`); its credentials are read from that provider's own env vars |
| `SEEDLY_FTP_TLS_ACME_EMAIL` | *(empty)* | ACME account email |
| `SEEDLY_FTP_TLS_ACME_CACHE_DIR` | `/data/certs` | Account key and certificate storage |
| `SEEDLY_FTP_TLS_ACME_CA_URL` | Let's Encrypt prod | ACME directory (use staging while testing) |

## FTP

Explicit FTPS (`AUTH TLS`) on `SEEDLY_FTP_LISTEN`; plaintext logins are refused unless
`SEEDLY_FTP_ALLOW_PLAINTEXT=true`. Log in with your Seedly username and password.

- **Scope:** each user only sees their own torrents' files (the root lists the content of their torrents). Members are read-only; admins can also upload, rename, delete and create folders. The torrent's files are also listed in its detail panel in the web UI, where they can be selected and downloaded (any user's torrent for admins, via "View as").
- **Brute force:** failed logins (web and FTP) are throttled per IP — see `SEEDLY_LOGIN_MAX_FAILURES` / `SEEDLY_LOGIN_BAN`.
- **Ports:** publish the control port (e.g. `21:2121`) and the whole `SEEDLY_FTP_PASV_PORTS` range, and open them in your firewall. Set `SEEDLY_FTP_PUBLIC_HOST` to the public hostname.
- **TLS** — pick one:
  - *Existing certificate files:* mount them and set `SEEDLY_FTP_TLS_CERT_FILE` / `SEEDLY_FTP_TLS_KEY_FILE` (e.g. certbot output copied by a deploy hook so the container user can read the key). They are reloaded when the files change.
  - *Built-in Let's Encrypt:* set `SEEDLY_FTP_TLS_ACME_DOMAIN`, `..._DNS_PROVIDER` and the provider's credential env vars (DNS-01). Try the staging CA first via `SEEDLY_FTP_TLS_ACME_CA_URL`.
