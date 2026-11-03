# Deploying on a server (any cloud)

One script puts Acnologia on any Linux server: it installs Docker, runs the
server, and puts Caddy in front with a Let's Encrypt certificate.

You need:

- A Linux VM that Docker supports: Ubuntu 22.04 or 24.04, Debian 12, Fedora,
  the RHEL family, Amazon Linux and the rest.
- 1 vCPU and 512 MB to 1 GB of RAM. That is enough.
- Ports 80 and 443 reachable from the internet (Let's Encrypt checks port 80).
- Root, or a user with `sudo`.
- Optionally, a domain with a DNS A record pointing at the server. Without one
  you get a working HTTPS address anyway (see below).

The image is `ghcr.io/sidharthpunathil/acnologia`, built for `linux/amd64`
and `linux/arm64`, so Arm servers (Hetzner CAX, AWS Graviton, Oracle Ampere)
work the same way.

## One line install

With a domain that already points at the server:

```
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
  | sudo bash -s -- --domain key.example.com --email you@example.com
```

`--email` is optional: Let's Encrypt uses it to warn you about expiring
certificates.

Without a domain:

```
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
  | sudo bash
```

The installer looks up the server's public IPv4 address and serves
`https://<ip with dashes>.sslip.io`, for example
`https://203-0-113-7.sslip.io`. sslip.io is a free wildcard DNS name that
resolves to the IP written in it, so Caddy can get a real certificate before
you own a domain. Everyone on sslip.io shares the same Let's Encrypt rate
limits, so issuance can occasionally be refused for a while. It is fine to get
started; switch to your own domain later by running the installer again with
`--domain` (see [Point your domain](#point-your-domain)).

When it finishes it prints:

- the URL, `Acnologia is running at https://...`, with a note that the first
  visit can take up to a minute while Caddy gets the certificate;
- on the first install only, the **access password**, with a reminder that it
  sits in `/opt/acnologia/.env` next to `ACNOLOGIA_MASTER_KEY`. Copy both to
  your password manager now;
- how to upgrade, read the logs and back up.

Open the URL and type the access password. That is the whole login: you land
in the dashboard as the owner (see [Deploying on Railway](deploy-railway.md)
for how the lock works).

## What the installer does

1. Installs Docker with `get.docker.com` if it is missing, enables the
   service, and checks that the `docker compose` plugin is there.
2. Picks the address: `--domain`, or `<ip>.sslip.io`, or with `--no-tls` the
   `--public-url` (default `http://<public ip>:<port>`).
3. Creates `/opt/acnologia` (mode 700) and writes the files below.
4. Opens the ports in the host firewall if one is active: `ufw` or
   `firewalld`. It opens 80/tcp, 443/tcp and 443/udp, or only `--port` with
   `--no-tls`. Cloud firewalls (security groups) are yours to open.
5. Pulls the image. If the pull fails it uses a copy already on the server,
   and if there is none it builds the image from source (a few minutes).
6. Starts everything with `docker compose up -d --remove-orphans` and waits
   up to two minutes for `/healthz`. If the server never answers it prints
   the last 50 log lines and stops.

The files in `/opt/acnologia`:

| File | What it is |
|---|---|
| `.env` | The server settings, mode 600. Written **once**, on the first install, with a random `ACNOLOGIA_MASTER_KEY` (32 bytes, base64), a random 28 character `ACNOLOGIA_ACCESS_PASSWORD` and `ACNOLOGIA_REQUIRE_ACCESS_PASSWORD=true`. Later runs never replace it; they only rewrite a few lines (see below). Every variable is in [configuration](configuration.md). |
| `install.conf` | The options of the last run, so a rerun repeats them. |
| `compose.yml` | Rewritten on every run. Edit `.env` instead, or pass options to the installer. |
| `Caddyfile` | Rewritten on every run (not used with `--no-tls`). |
| `data/` | The database and anything else the server writes, mounted at `/data`. Owned by uid 65532, the image's non-root user. |
| `caddy/` | Caddy's certificates and state. |

The lines the installer rewrites in `.env` on every run:

- `ACNOLOGIA_PUBLIC_URL`, set to the address it serves.
- `ACNOLOGIA_SECURE_COOKIES` and `ACNOLOGIA_TRUST_PROXY_HEADERS`: both `true`
  with Caddy (Caddy terminates TLS and sets `X-Forwarded-For`). With
  `--no-tls` they are yours: a new install writes both as `false`, and a
  rerun only turns `SECURE_COOKIES` off again when the public URL is plain
  `http://`.
- `ANTHROPIC_API_KEY`, only when you pass `--anthropic-key` (or set the
  variable).

Everything else you add or change in `.env` stays.

The Acnologia container listens on `127.0.0.1:8080` on the host (loopback
only, for the health check and ssh tunnels). Only Caddy is public, on ports
80 and 443 (443/udp too, for HTTP/3).

## Options

Each option also works as an environment variable. The command line wins over
the environment, and both win over `install.conf`.

| Option | Variable | What it does |
|---|---|---|
| `--domain NAME` | `ACN_DOMAIN` | Public host name pointing at this server. Default `<public ip>.sslip.io`. |
| `--email ADDR` | `ACN_EMAIL` | Let's Encrypt contact address. |
| `--no-tls` | `ACN_NO_TLS=1` | No Caddy: plain HTTP on `--port`, for a server behind your own proxy or load balancer. |
| `--public-url URL` | `ACN_PUBLIC_URL` | The address users reach, with `--no-tls`. Ignored without it (the URL is then `https://<domain>`). |
| `--port N` | `ACN_PORT` | Host port, default `8080`. Loopback only unless `--no-tls`. |
| `--dir PATH` | `ACN_DIR` | Install directory, default `/opt/acnologia`. Not remembered: pass it on every run. |
| `--image REF` | `ACN_IMAGE` | Image to run, default `ghcr.io/sidharthpunathil/acnologia:latest`. |
| `--build` | `ACN_BUILD=1` | Build the image from source instead of pulling it. |
| `--ref REF` | `ACN_REF` | Git branch or tag to build, default `main`. |
| `--anthropic-key KEY` | `ANTHROPIC_API_KEY` | Turns on the AI features. Written to `.env`, not to `install.conf`. |
| `--uninstall` | | Stops and removes the containers. Keeps `data/` and `.env`. |
| `-h`, `--help` | | Shows the help. |

`install.conf` remembers the domain (unless it was an sslip.io name, so a
rerun after an IP change picks the new one), the email, `--no-tls`, the port,
the public URL (only with `--no-tls`, and only one you passed: one worked out
from the server's IP is worked out again on every run), the image (unless `--build`), `--build`
and the ref. To undo a remembered switch, set it explicitly:
`ACN_NO_TLS=0` turns Caddy back on, `ACN_BUILD=0` goes back to pulling.

`sudo` drops your environment, so put variables after it:
`curl ... | sudo ACN_DOMAIN=key.example.com bash`.

## Behind your own proxy or load balancer

If something else terminates TLS (a cloud load balancer, nginx, Traefik,
Cloudflare Tunnel), skip Caddy:

```
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
  | sudo bash -s -- --no-tls --port 8080 --public-url https://key.example.com
```

The server is then published on `0.0.0.0:<port>` over plain HTTP. Let only
your proxy reach that port (cloud firewall or security group). Docker's
published ports skip `ufw` rules, so a host firewall alone does not close it.

With `--no-tls` a new install writes `ACNOLOGIA_SECURE_COOKIES=false` and
`ACNOLOGIA_TRUST_PROXY_HEADERS=false`. Change them in `/opt/acnologia/.env`:

- `ACNOLOGIA_TRUST_PROXY_HEADERS=true` only if your proxy **overwrites**
  `X-Forwarded-For`. If it passes a client supplied header through, clients
  can spoof their address and dodge the sign in rate limits.
- `ACNOLOGIA_SECURE_COOKIES=true` if the proxy serves HTTPS.

Then apply them:

```
docker compose --project-directory /opt/acnologia up -d
```

A rerun of the installer keeps both, with one exception: when the public URL
starts with `http://` it turns `ACNOLOGIA_SECURE_COOKIES` off again, because a
browser drops `Secure` cookies over plain HTTP. So with a proxy that serves
HTTPS, pass `--public-url https://key.example.com` once; it is remembered.

## Cloud init (any provider)

Most clouds run a cloud-init script on first boot. Paste
[cloud-init.yaml](../deploy/cloud-init.yaml) as the server's user data and
the server installs itself. Fill in `ACN_DOMAIN` and `ACN_EMAIL` in the file
first if you have them; empty, it uses sslip.io.

Where the field is:

| Provider | Field |
|---|---|
| AWS EC2 | Launch instance, Advanced details, User data |
| Google Cloud | Ubuntu images read the `user-data` metadata key (Metadata, add item `user-data`) |
| Azure | Advanced tab, Custom data (cloud-init) |
| DigitalOcean | Advanced options, Add initialization scripts |
| Hetzner Cloud | Cloud config |
| Vultr | Cloud-Init User-Data |
| Linode / Akamai | Add User Data |
| Oracle Cloud | Show advanced options, Management, cloud-init script |
| Scaleway | Advanced options, cloud-init |

To pass options as arguments instead of editing the file, generate the user
data with [user-data.sh](../deploy/lib/user-data.sh):

```
deploy/lib/user-data.sh --domain key.example.com --email you@example.com > user-data.yaml
```

It takes the installer's options (any it does not know are passed through as
they are). Both kinds of user data save the installer to
`/root/acnologia-install.sh` before running it, and log every step, failed
downloads included, to `/var/log/acnologia-install.log`.

User data is stored in the provider's metadata in plain text, readable from
the console and, on most clouds, from inside the server. Leave
`ANTHROPIC_API_KEY` out of it and add the key to `/opt/acnologia/.env`
afterwards, then run `docker compose --project-directory /opt/acnologia up -d`.

The install takes 2 to 4 minutes after boot. Follow it with:

```
tail -f /var/log/acnologia-install.log
```

Read the access password with `grep ACCESS_PASSWORD /opt/acnologia/.env`
(prefix `sudo` on clouds where you log in as `ubuntu`, `ec2-user` or
another default user rather than root).

## Hetzner Cloud, one command

[hetzner.sh](../deploy/hetzner.sh) creates the server from your machine with
the `hcloud` CLI.

1. Install `hcloud`: `brew install hcloud`, or from
   [github.com/hetznercloud/cli](https://github.com/hetznercloud/cli).
2. In the Hetzner Console, open your project, Security, API tokens, and make a
   Read and Write token. Then:

   ```
   hcloud context create acnologia
   ```

   and paste the token.
3. Run:

   ```
   deploy/hetzner.sh --domain key.example.com --email you@example.com
   ```

What it does:

- Uploads your SSH public key if Hetzner does not have it yet (default
  `~/.ssh/id_ed25519.pub`, then `id_ecdsa.pub`, then `id_rsa.pub`; or
  `--ssh-key PATH`).
- Creates a firewall named `<name>-web` that allows 22, 80 and 443 over TCP
  and 443 over UDP, unless one with that name exists.
- Creates an `ubuntu-24.04` server named `acnologia` in `fsn1`, with the
  firewall and cloud-init user data that runs the installer. Without `--type`
  it tries `cx23` (x86), then `cax11` (Arm), then `cpx12`, since the cheap
  types sell out now and then and not every type exists in every location.
  Hetzner retired `cx22` at the start of 2026; `cx23` replaced it.
- Waits up to about 7 minutes for `https://<domain or sslip.io name>/healthz`
  to answer, then prints the URL and the commands to read the password and the
  master key.

Options: `--name`, `--type`, `--location` (`fsn1`, `nbg1`, `hel1`, `ash`,
`hil`, `sin`), `--ssh-key`, and `--domain`, `--email`, `--anthropic-key`,
`--image`, `--ref`, which go to the installer. `HCLOUD_SERVER_TYPE` and
`HCLOUD_LOCATION` work as defaults. It stops if a server with the same name
already exists.

Read the access password:

```
ssh root@<ip> grep ACCESS_PASSWORD /opt/acnologia/.env
```

With `--domain`, point the A record at the printed IP as soon as the script
shows it: the certificate, and so the script's wait, only succeed once DNS
resolves. If the wait times out, the script prints how to read the install
log.

## DigitalOcean, one command

[digitalocean.sh](../deploy/digitalocean.sh) creates a Droplet with `doctl`.

1. Install `doctl`: `brew install doctl`, or see
   [the doctl docs](https://docs.digitalocean.com/reference/doctl/how-to/install/).
2. Make a read and write API token in the DigitalOcean control panel, then:

   ```
   doctl auth init
   ```

3. Run:

   ```
   deploy/digitalocean.sh --domain key.example.com --email you@example.com
   ```

What it does:

- Uploads your SSH public key if DigitalOcean does not have it yet.
- Creates a Droplet named `acnologia`: `s-1vcpu-1gb` ($6 a month) in `nyc3`,
  image `ubuntu-24-04-x64`, tagged `acnologia`, with monitoring on and
  Droplet backups on (they add 20% to the price; `--no-backups` skips them).
  The user data runs the installer.
- Creates a cloud firewall named `<name>-web` on the `acnologia` tag (22, 80
  and 443 over TCP, 443 over UDP in; everything out), unless one with that
  name exists.
- Waits for the site to answer over HTTPS, like the Hetzner script.

Options: `--name`, `--size` (`s-1vcpu-512mb-10gb` at $4 also runs it),
`--region` (`nyc3`, `sfo3`, `ams3`, `fra1`, `lon1`, `sgp1`, `blr1`, `syd1`,
`tor1`), `--no-backups`, `--ssh-key`, and the installer options as above.
`DO_SIZE` and `DO_REGION` work as defaults.

App Platform is not an option: it has
[no persistent volumes](https://docs.digitalocean.com/products/app-platform/how-to/store-data/),
so every deploy would start with an empty database.

## Point your domain

1. At your DNS provider, add an A record for your name (say
   `key.example.com`) with the server's IPv4 address. Add an AAAA record only
   if the server has IPv6 and you want it used.
2. Wait until `dig +short key.example.com` returns that address.
3. Run the installer again with the domain:

   ```
   curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
     | sudo bash -s -- --domain key.example.com --email you@example.com
   ```

Caddy gets the certificate on the first request. The installer also updates
`ACNOLOGIA_PUBLIC_URL`, and remembers the domain for later runs. Your data and
password stay.

## Upgrades

Run the installer again:

```
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh | sudo bash
```

On servers set up through cloud-init (`cloud-init.yaml`, `user-data.sh`,
`hetzner.sh` or `digitalocean.sh`), `sudo bash /root/acnologia-install.sh`
does the same with the installer saved at
first boot. It still pulls the newest image; the one-liner also gets the
newest installer.

`install.conf` keeps your options, so no flags are needed. It pulls the image,
recreates the containers and waits for the health check. Database migrations
run on start (see [Upgrading](configuration.md#upgrading)).

Pin a version with `--image`:

```
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
  | sudo bash -s -- --image ghcr.io/sidharthpunathil/acnologia:0.1.0
```

The pin is remembered. Pass `--image ghcr.io/sidharthpunathil/acnologia:latest`
to follow releases again.

## Backups

Everything lives in `/opt/acnologia/data`:

- `acnologia.db` with its `acnologia.db-wal` and `acnologia.db-shm` files
  (`cogito.db` on an install from before the rename);
- `master.key`, only if `ACNOLOGIA_MASTER_KEY` is not set (the installer sets
  it in `.env`, so normally there is none);
- `access-password`, only if one was generated.

The master key in `/opt/acnologia/.env` decrypts every stored API key. A
database backup without it is useless, and Acnologia refuses to start against
a database written with another key. Keep `.env` in your password manager or
somewhere other than the database backups, so one stolen copy is not enough.

A consistent backup, with a few seconds of downtime:

```
docker compose --project-directory /opt/acnologia stop acnologia
tar -czf /root/acnologia-$(date +%F).tar.gz -C /opt/acnologia data
docker compose --project-directory /opt/acnologia start acnologia
```

A live backup, no downtime, with the `sqlite3` tool on the host:

```
apt install sqlite3        # dnf install sqlite on Fedora and RHEL
sqlite3 /opt/acnologia/data/acnologia.db ".backup /root/acnologia-$(date +%F).db"
```

To run it daily at 03:30 and keep two weeks, save this as
`/etc/cron.d/acnologia-backup` (cron needs `%` escaped):

```
30 3 * * * root mkdir -p /root/backups && sqlite3 /opt/acnologia/data/acnologia.db ".backup /root/backups/acnologia-$(date +\%F).db" && find /root/backups -name 'acnologia-*.db' -mtime +14 -delete
```

Copies on the same disk do not survive losing the server: ship them off it
too. Provider snapshots help with that. `digitalocean.sh` turns on Droplet
backups by default; on Hetzner, `hcloud server enable-backup acnologia` turns
on daily backups (20% of the server price). A snapshot taken while the server
runs is like a power cut for SQLite, which recovers from it, but the
`.backup` copies above are the safer restore point.

## Moving to another server

1. On the old server, stop Acnologia and pack the data and settings:

   ```
   docker compose --project-directory /opt/acnologia stop acnologia
   tar -czf /root/acnologia-move.tar.gz -C /opt/acnologia data .env
   ```

2. Copy the archive to the new server and unpack it:

   ```
   mkdir -p /opt/acnologia
   tar -xzf acnologia-move.tar.gz -C /opt/acnologia
   chown -R 65532:65532 /opt/acnologia/data
   ```

   The container runs as uid 65532. The installer also runs
   `chown -R` on `data` every time, so this step only matters if you start
   the containers by hand before running it.
3. Point the DNS record at the new IP.
4. Run the installer with the same `--domain`. It finds `.env`, keeps the
   master key and password, and starts on your data.

## Troubleshooting

**The certificate is not issued.** The site does not load over HTTPS, or the
browser warns. Check that the domain resolves to this server
(`dig +short key.example.com`), and that ports 80 and 443 are open in the
cloud firewall or security group: Let's Encrypt has to reach port 80. Caddy's
log says what failed:

```
docker compose --project-directory /opt/acnologia logs caddy
```

On an sslip.io name, a rate limit error means waiting or switching to your
own domain.

**The server does not become healthy.** The installer prints the last 50 log
lines. For more:

```
docker compose --project-directory /opt/acnologia logs acnologia
```

**"the master key does not match this database".** `ACNOLOGIA_MASTER_KEY` in
`.env` is not the key the database was created with. This happens after
restoring a database without its `.env`, or after a fresh install over old
data. Put the original key back in `.env` and run
`docker compose --project-directory /opt/acnologia up -d`.

**A cloud init install never comes up.** Read
`/var/log/acnologia-install.log` on the server.

**Uninstall.**

```
curl -fsSL https://raw.githubusercontent.com/sidharthpunathil/acnologia/main/deploy/install-server.sh \
  | sudo bash -s -- --uninstall
```

It stops and removes the containers and keeps `/opt/acnologia`. Delete that
folder to remove the data and settings too (back up `.env` first if you might
want the data again).
