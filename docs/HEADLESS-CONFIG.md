# Headless Mode — JSON Config Schema

Knuckle's headless mode (`--headless --config <file.json>`) performs a fully
automated, non-interactive install driven by a JSON configuration file.
Both Flatcar Linux and Fedora CoreOS (FCOS) are supported via the `os` field,
as is [uCore](https://github.com/ublue-os/ucore) — see the [uCore section](#ucore)
for how it is installed and why it needs no ISO.
This document is the authoritative reference for every field in that file.

## Quick-start examples

### Minimal DHCP install

```json
{
  "channel": "stable",
  "hostname": "flatcar-node",
  "disk": "/dev/sda",
  "network": { "mode": "dhcp" },
  "users": [
    {
      "username": "core",
      "ssh_keys": ["ssh-ed25519 AAAA... you@host"]
    }
  ],
  "update_strategy": "reboot"
}
```

### Static IP with sysexts and Tailscale

```json
{
  "channel": "stable",
  "hostname": "k8s-worker-1",
  "disk": "/dev/disk/by-id/ata-Samsung_SSD_870_EVO_S6ENNX0T123456",
  "network": {
    "mode": "static",
    "interface": "enp3s0",
    "address": "192.168.1.50/24",
    "gateway": "192.168.1.1",
    "dns": ["1.1.1.1", "8.8.8.8"]
  },
  "users": [
    {
      "username": "core",
      "github_user": "yourgithubuser",
      "groups": ["sudo", "docker"]
    }
  ],
  "sysexts": ["docker", "kubernetes"],
  "tailscale": {
    "auth_key": "tskey-auth-kXXXXXXXXX-XXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX",
    "mode": "connect"
  },
  "update_strategy": "reboot",
  "reboot": true
}
```

### Dry-run (CI / testing)

```json
{
  "channel": "stable",
  "hostname": "ci-test",
  "disk": "/dev/sda",
  "network": { "mode": "dhcp" },
  "users": [{ "username": "core", "ssh_keys": ["ssh-ed25519 AAAA... ci@host"] }],
  "update_strategy": "reboot",
  "dry_run": true
}
```

### Fedora CoreOS (FCOS) install

```json
{
  "os": "fcos",
  "channel": "stable",
  "hostname": "fcos-node-1",
  "disk": "/dev/disk/by-id/...",
  "network": { "mode": "dhcp" },
  "users": [
    {
      "username": "core",
      "ssh_keys": ["ssh-ed25519 AAAA... you@host"]
    }
  ]
}
```

> **FCOS limitations** (compared to Flatcar):
> - `version` — version pinning is not supported by `coreos-installer`; the field is silently ignored.
> - `nvidia_driver_version` — not supported; rejected with an error if set.
> - `update_strategy` — `etcd-lock` is not available; use `reboot` (maps to zincati `immediate`) or `off` (disables zincati automatic updates). Defaults to `reboot`.

### uCore install

[uCore](https://github.com/ublue-os/ucore) is an OCI image that extends Fedora
CoreOS with cockpit, ZFS, Samba, tailscale and friends.

**uCore publishes no ISO.** Its GitHub releases contain changelogs only, the
images live on `ghcr.io`, and the project states that it does not ship its own
installer. knuckle therefore installs uCore the way uCore documents: the target
disk receives a Fedora CoreOS deployment via `coreos-installer`, and an Ignition
oneshot unit rebases it onto the uCore OCI image on **first boot**.

```json
{
  "os": "ucore",
  "ucore": {
    "image": "ucore",
    "stream": "stable",
    "nvidia": "nvidia-lts",
    "verify": "signed"
  },
  "hostname": "ucore-node-1",
  "disk": "/dev/disk/by-id/...",
  "network": { "mode": "dhcp" },
  "users": [
    {
      "username": "core",
      "ssh_keys": ["ssh-ed25519 AAAA... you@host"]
    }
  ]
}
```

The whole `ucore` block is optional — omitted, `{}` gives `ucore:stable` with a
signature-verified rebase and no NVIDIA driver.

> **uCore limitations** (compared to Flatcar and FCOS):
> - `channel` is **ignored**. uCore's stream vocabulary is its own (`stable`,
>   `testing`, `lts`) and lives in `ucore.stream`. The Fedora CoreOS base stream
>   that `coreos-installer` lays down is derived from it, so there is only ever
>   one stream knob.
> - `sysexts` — the bakery serves Flatcar sysexts built against Flatcar kernels,
>   which do not load on a Fedora kernel. The field is ignored for uCore.
> - `nvidia_driver_version` — not supported; rejected with an error if set. Use
>   `ucore.nvidia`, which selects a driver baked into the image tag.
> - `update_strategy` — `etcd-lock` is not available. uCore disables zincati and
>   updates via `rpm-ostreed`/`bootc`, so knuckle writes no update config at all.
> - `version` — ignored with a warning; uCore images are stream-tagged.
> - `ignition_url` — an external config **replaces** knuckle's provisioning, so
>   the uCore rebase unit is not applied and the target stays on stock CoreOS
>   unless your own config performs the rebase. knuckle logs a warning.
> - SecureBoot: the `nvidia` and `zfs` drivers need ublue-os's MOK imported
>   (`sudo mokutil --import /etc/pki/akmods/certs/akmods-ublue.der`) after first
>   boot, otherwise those drivers fail to load.

---

## Full field reference

### Top-level fields

| Field | Type | Required | Default | Description |
|-------|------|----------|---------|-------------|
| `os` | string | no | `"flatcar"` | Target OS. One of `flatcar` (default), `fcos`, `ucore`, `bluefin-ddi`. Omit for Flatcar (backward compatible). |
| `ucore` | object | no | _(defaults)_ | See [uCore](#ucore). Only read when `os` is `ucore`; every field is optional. |
| `channel` | string | no | `"stable"` | Release channel. Flatcar: one of `stable`, `beta`, `alpha`, `lts`, `edge`. FCOS: one of `stable`, `testing`, `next`. **Ignored for uCore** — use `ucore.stream`. |
| `version` | string | no | _(latest)_ | Pin to a specific Flatcar version, e.g. `"3510.2.8"`. Omit to use the latest for the channel. **Not supported for FCOS or uCore** — `coreos-installer` has no equivalent version-pinning flag for stream-based installs; this field is silently ignored when `os` is `fcos` or `ucore`. |
| `hostname` | string | yes | — | Machine hostname. Must be a valid RFC 1123 hostname label. |
| `disk` | string | yes* | — | Target disk path. Use a stable `/dev/disk/by-id/...` path in production. `*` Not required when `ignition_url` is set. |
| `network` | object | yes | — | See [Network](#network). |
| `users` | array | yes* | — | One or more user accounts. `*` Not required when `ignition_url` is set. |
| `update_strategy` | string | no | `"reboot"` | OS update strategy. Flatcar: one of `reboot`, `off`, `etcd-lock`. FCOS: one of `reboot` (maps to zincati `immediate`) or `off` (disables zincati automatic updates). `etcd-lock` is not supported for FCOS or uCore; uCore ignores the field entirely and updates via rpm-ostreed. |
| `arch` | string | no | `"amd64"` | CPU architecture. One of `amd64`, `arm64`. `arm64` is not available on Flatcar's `lts` channel. uCore's `lts` stream is available on both. |
| `timezone` | string | no | `"UTC"` | System timezone (IANA format, e.g. `"America/New_York"`). |
| `sysexts` | string[] | no | `[]` | List of system extension names from the bakery catalog (e.g. `["docker", "kubernetes"]`). Ignored for FCOS and uCore — Flatcar bakery sysexts do not load on a Fedora kernel. |
| `nvidia_driver_version` | string | no | _(none)_ | **Flatcar only.** NVIDIA kernel driver series. One of `570-open` (default/recommended), `550-open`, `535-open`, `460`. Omit to skip NVIDIA setup. Rejected with an error if set for FCOS or uCore — uCore uses `ucore.nvidia` instead. |
| `tailscale` | object | no | — | See [Tailscale](#tailscale). Omit or leave `auth_key` blank to skip. |
| `wifi` | object | no | _(skipped)_ | See [WiFi](#wifi). The TUI offers this step with `nmtui`; headless config supplies the keyfiles directly. |
| `swap` | object | no | _(enabled, 4 GiB)_ | See [Swap](#swap). Omit for the default (4 GiB enabled). |
| `ignition_url` | string | no | — | URL of an external Ignition config. When set, knuckle downloads this config instead of generating one — only `disk` is then required. Must be HTTPS. |
| `reboot` | bool | no | `false` | If `true`, reboot immediately after a successful install. |
| `dry_run` | bool | no | `false` | If `true`, simulate the install without writing to disk. Safe for CI and testing. |

---

### Network

The `network` object configures the installed system's network. Set `mode` to
`"dhcp"` for automatic configuration or `"static"` for a fixed IP.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `mode` | string | yes | `"dhcp"` or `"static"` |
| `interface` | string | static only | Network interface name, e.g. `"enp3s0"` |
| `address` | string | static only | IP address with CIDR mask, e.g. `"192.168.1.50/24"` |
| `gateway` | string | static only | Default gateway IP, e.g. `"192.168.1.1"` |
| `dns` | string[] | optional | DNS server IPs. Defaults to gateway if omitted. |

**DHCP example:**
```json
"network": { "mode": "dhcp" }
```

**Static example:**
```json
"network": {
  "mode": "static",
  "interface": "eth0",
  "address": "10.0.0.10/24",
  "gateway": "10.0.0.1",
  "dns": ["1.1.1.1", "8.8.8.8"]
}
```

---

### Users

The `users` array defines one or more accounts to create. At least one account
is required unless `ignition_url` is set.

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `username` | string | yes | Login name. Must be a valid POSIX username. |
| `ssh_keys` | string[] | no* | List of SSH public key strings. |
| `github_user` | string | no* | GitHub username — knuckle fetches all public keys at install time. |
| `password` | string | no* | Pre-hashed password in crypt format (`$6$`, `$y$`, `$2b$`, `$5$`). **Not plaintext.** |
| `groups` | string[] | no | Additional groups. Defaults to `["sudo", "docker"]`. |

`*` Each user must have at least one of `ssh_keys`, `github_user`, or `password`.

**Generate a password hash:**
```sh
openssl passwd -6          # SHA-512 ($6$…)
mkpasswd --method=yescrypt # yescrypt ($y$…)
```

**Example with GitHub key fetch:**
```json
"users": [
  {
    "username": "core",
    "github_user": "octocat",
    "groups": ["sudo", "docker"]
  }
]
```

**Example with explicit SSH key:**
```json
"users": [
  {
    "username": "core",
    "ssh_keys": [
      "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAA... user@host"
    ]
  }
]
```

---

### uCore

Only read when `os` is `ucore`. Every field is optional; omitted values take the
defaults below. All four combine into a single image reference that the
installed system rebases onto at first boot.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `image` | string | `"ucore"` | Image family: `ucore` (full server image — cockpit, ZFS, Samba, mergerfs), `ucore-minimal` (slim), or `ucore-hci` (adds libvirt / cockpit-machines). |
| `stream` | string | `"stable"` | Release stream: `stable`, `testing`, or `lts` (stable base with a longterm kernel). Replaces the top-level `channel` for this target. |
| `nvidia` | string | `""` (none) | NVIDIA driver baked into the image tag: `""`, `nvidia` (latest open), or `nvidia-lts` (LTS driver, required for Maxwell/Pascal). |
| `verify` | string | `"signed"` | `signed` makes rpm-ostree enforce the image's sigstore signature (`ostree-image-signed:docker://`); `unverified` does not (`ostree-unverified-registry:`). |

**Resulting reference** — `<transport>ghcr.io/ublue-os/<image>:<stream>[-<nvidia>]`:

| `ucore` block | Image reference |
|---------------|-----------------|
| _(omitted)_ | `ostree-image-signed:docker://ghcr.io/ublue-os/ucore:stable` |
| `{"image":"ucore-minimal","stream":"lts","nvidia":"nvidia-lts"}` | `ostree-image-signed:docker://ghcr.io/ublue-os/ucore-minimal:lts-nvidia-lts` |
| `{"verify":"unverified"}` | `ostree-unverified-registry:ghcr.io/ublue-os/ucore:stable` |

`lts` is a uCore stream with no FCOS equivalent — it is FCOS *stable* with a
longterm kernel — so the base stream `coreos-installer` lays down is derived as
`stable`. `testing` maps to `testing`.

Verify a rebuilt image with uCore's own key:

```bash
cosign verify --key https://github.com/ublue-os/ucore/raw/main/cosign.pub \
  ghcr.io/ublue-os/ucore:stable
```

---

### WiFi

The TUI's WiFi step hands the terminal to `nmtui`, then copies the
NetworkManager keyfiles it produced onto the target so the installed system
comes up already joined. Headless installs have no terminal, so the keyfiles are
supplied directly.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `profiles` | object[] | `[]` | NetworkManager keyfiles. Each key is the basename written to `/etc/NetworkManager/system-connections/`. |
| `profiles[].filename` | string | — | Keyfile basename, e.g. `home.nmconnection`. Must match `[A-Za-z0-9][A-Za-z0-9._-]*` and contain no `..`. |
| `profiles[].contents` | string | — | The keyfile body, verbatim, **including the PSK**. Treat this as a secret. |

```json
"wifi": {
  "profiles": [
    {
      "filename": "home.nmconnection",
      "contents": "[connection]\nid=HomeWifi\ntype=wifi\n\n[wifi]\nssid=HomeWifi\nmode=infrastructure\n\n[wifi-security]\nkey-mgmt=wpa-psk\npsk=YOUR_PASSWORD_HERE\n"
    }
  ]
}
```

knuckle writes each profile `0600`. That is not cosmetic: NetworkManager refuses
a group- or world-readable keyfile, so a `0644` profile would be silently ignored
and the machine would boot with no network while looking correctly provisioned.

> **Applies to Fedora CoreOS and uCore only.** Those use NetworkManager. Flatcar
> uses systemd-networkd, and Bluefin Server is a DDI image with no Ignition — on
> both, the profiles are written but nothing reads them. The TUI warns; headless
> does not fail.

---

### Tailscale

| Field | Type | Required | Description |
|-------|------|----------|-------------|
| `auth_key` | string | yes (if section present) | Tailscale pre-auth key (`tskey-auth-…`). Leave blank or omit the section to skip. |
| `mode` | string | no | `"connect"` (default), `"exit-node"`, or `"subnet-router"` |
| `routes` | string | subnet-router only | Comma-separated CIDRs to advertise, e.g. `"10.0.0.0/24,192.168.1.0/24"` |

---

### Swap

By default (when `swap` is omitted), knuckle creates a 4 GiB swap file at
`/var/swapfile`. To customise or disable swap, include the `swap` object.

| Field | Type | Default | Description |
|-------|------|---------|-------------|
| `enabled` | bool | `true` | `false` disables swap entirely. |
| `size_mb` | int | `4096` | Swap file size in MiB. `0` uses the default (4 GiB). Maximum: 32768 MiB (32 GiB). |

**Disable swap:**
```json
"swap": { "enabled": false }
```

**Custom size:**
```json
"swap": { "enabled": true, "size_mb": 8192 }
```

---

## External Ignition URL mode

When `ignition_url` is set, knuckle downloads the Ignition config from that
URL instead of generating one from `users`, `network`, `sysexts`, etc. In this
mode only `disk` is required; all other config fields (users, network, sysexts,
tailscale) are ignored.

```json
{
  "channel": "stable",
  "disk": "/dev/sda",
  "ignition_url": "https://config.example.com/node.ign",
  "update_strategy": "reboot"
}
```

The URL must be HTTPS and reachable from the installer environment.

---

## Validation rules

Knuckle validates the config before touching the disk. Key rules:

- `channel` must be one of `stable`, `beta`, `alpha`, `lts`, `edge`
- `version`, if set, must match `X.Y.Z` (all numeric components)
- `hostname` must be a valid RFC 1123 hostname label (no dots; max 63 chars)
- `disk` must start with `/dev/` and not contain `..` path traversal
- Static network: `interface`, `address` (CIDR), and `gateway` are all required
- DNS entries must be valid IP addresses
- Each user needs a valid POSIX username and at least one auth method
- `password` must be a valid crypt hash (not plaintext)
- `update_strategy` must be `reboot`, `off`, or `etcd-lock`
- `nvidia_driver_version` must be one of `570-open`, `550-open`, `535-open`, `460`
- `swap.size_mb` must be between 0 and 32768 (MiB)
- `tailscale.auth_key` must begin with `tskey-auth-`
- `ignition_url` must be an HTTPS URL

---

## FCOS version pinning

When `os` is `fcos`, the `version` field is **not supported** in v1. `coreos-installer`
installs the latest image for the requested stream by default; there is no `-V` equivalent
for stream-based version pinning without constructing an explicit `--image-url`.

If `version` is set alongside `os: fcos`, knuckle logs a warning and proceeds with the
stream default (i.e., the version value is silently ignored). A future release may support
explicit image URLs for reproducible FCOS installs.

The same applies to `os: ucore`, for a stronger reason: uCore images are tagged by
stream rather than release (`ghcr.io/ublue-os/ucore:stable`), so there is no version
string to pin. Use `ucore.stream` instead.

---

## CLI flags override JSON

Two CLI flags can override JSON config fields:

```sh
# Force dry-run even if the config file has "dry_run": false
knuckle --headless --config install.json --dry-run

# Override the log file path (default: /tmp/knuckle.log)
knuckle --headless --config install.json --log-file /var/log/knuckle.log
```
