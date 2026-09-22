# Install on Linux

Use a 64-bit Linux machine: AMD64 for x86-64 PCs/servers, ARM64 for Raspberry Pi OS 64-bit and ARM servers. Release binaries need no Go compiler, Node installation or database server. Root access is only needed for system-wide installation.

## Try it without installing a service

Download the archive for your architecture and `checksums.txt` from [Releases](https://github.com/rowesk/cairn/releases/latest). Run `uname -m`: `x86_64` means AMD64, `aarch64` means ARM64.

```sh
sha256sum --ignore-missing --check checksums.txt
tar -xzf cairn_v0.1.0_linux_arm64.tar.gz
cd cairn_v0.1.0_linux_arm64
./cairn version
./cairn init -data ./data -owner "Alex"
./cairn -data ./data -publisher-token-file ./data/publisher-token
```

Use the AMD64 filename instead on x86-64. Verify the archive reports `OK`. Run these commands from a directory you own. `init` creates the database and a mode-0600 credential. Change the author later in Settings; rerunning setup does not change an existing author or credential. If the token for an existing database is missing, setup refuses to silently replace it. Restore the original token or pass its actual path.

Open `http://127.0.0.1:8080` locally. For a remote machine, forward the port while setting up:

```sh
ssh -L 8080:127.0.0.1:8080 your-user@your-server
```

Then open `http://127.0.0.1:8080` on your own computer. If port 8080 is already used on the server, pass another loopback port with `-listen` and adjust the tunnel.

## Run with systemd

From the extracted release directory, on a new installation:

```sh
sudo useradd --system --home-dir /var/lib/cairn --shell /usr/sbin/nologin cairn
sudo install -d -m 0700 -o cairn -g cairn /var/lib/cairn
sudo install -m 0755 cairn /usr/local/bin/cairn
sudo -u cairn /usr/local/bin/cairn init -data /var/lib/cairn -owner "Alex"
sudo install -d -m 0755 /etc/cairn
sudo install -m 0644 deploy/cairn.env /etc/cairn/cairn.env
sudo install -m 0644 deploy/cairn.service /etc/systemd/system/cairn.service
sudo systemctl daemon-reload
sudo systemctl enable --now cairn
sudo systemctl status cairn
```

These are first-install instructions. Use the [upgrade guide](operations.md) for an existing installation. Do not recreate its credential or overwrite its configuration.

The service runs as `cairn`, stores data under `/var/lib/cairn` and initially listens on `127.0.0.1:8080`. Settings and author photos live in the database. Use `journalctl -u cairn` for service logs.

## Access from your devices

Install Tailscale on the server and your devices, sign them into your tailnet, and restrict access to this service to the owner. With the default localhost listener:

```sh
sudo tailscale serve --bg http://127.0.0.1:8080
```

Open the private HTTPS address Tailscale prints. Follow [Tailscale Serve's documentation](https://tailscale.com/docs/reference/tailscale-cli/serve) if its initial setup needs approval. This grants library and management access to whoever your tailnet policy permits. Do not use Tailscale Funnel for the private listener.

Alternatively, set `CAIRN_LISTEN` in `/etc/cairn/cairn.env` to the server's own Tailscale IP and a port, then restart Cairn. Cairn checks that the address belongs to the machine. Wildcard, ordinary LAN and public listening addresses are refused.

## Configure publishing

Open Settings through the private address your agents will use. Add an agent, create its key and copy the setup instructions. Store the key separately from reusable instructions. Agents running outside your tailnet need a deliberately authorised private runner; they cannot publish through the public sharing domain.

The installation credential in `publisher-token` remains a compatibility credential with broader publishing authority than individual agent keys. Keep it private and normally give agents their own keys.
