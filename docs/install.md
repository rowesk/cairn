# Install

Cairn ships as a single binary for 64-bit Linux: AMD64 for x86-64 PCs and servers, ARM64 for Raspberry Pi OS 64-bit and ARM servers. It needs no Go compiler, Node installation or database server. Root access is only needed for the system-wide service. To run Cairn on a Mac for your own use, see [Run on a Mac](#run-on-a-mac).

## Try it without installing a service

Download the archive for your architecture and `checksums.txt` from [Releases](https://github.com/rowesk/cairn/releases/latest). Run `uname -m`: `x86_64` means AMD64, `aarch64` means ARM64.

```sh
sha256sum --ignore-missing --check checksums.txt
tar -xzf cairn_v0.2.0_linux_arm64.tar.gz
cd cairn_v0.2.0_linux_arm64
./cairn version
./cairn init -data ./data -owner "Alex"
./cairn serve -data ./data
```

Use the AMD64 filename instead on x86-64, and check that the archive reports `OK`. Run these commands from a directory you own.

`init` creates the database and an installation token in `./data/publisher-token` that only you can read, then prints the command to start Cairn. `serve` finds the token there on its own. `-owner` sets the default author for pages you write in Cairn; change it later in Settings. Running `init` again keeps the existing author and token and says so. If the database exists but its token is missing, `init` stops rather than make a new one. Restore the original token or pass its path with `-publisher-token-file`.

When Cairn starts it prints a few lines: the version, the data directory, whether sharing and Send to Kindle are on, and the address to open. Open `http://127.0.0.1:8080` on the same machine. Press Ctrl+C to stop it.

For a remote machine, forward the port while setting up:

```sh
ssh -L 8080:127.0.0.1:8080 your-user@your-server
```

Then open `http://127.0.0.1:8080` on your own computer. If port 8080 is already in use on the server, Cairn says so. Pass another loopback port with `-listen 127.0.0.1:8090` and adjust the tunnel.

Run `./cairn help` to see every flag. Each flag also has an environment variable: `-listen` is `CAIRN_LISTEN`, `-public-base-url` is `CAIRN_PUBLIC_BASE_URL`, and so on. A flag wins over its variable.

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

`init` prints a command to start Cairn by hand. Skip it here: the service starts Cairn for you.

These are first-install instructions. For an existing installation, follow the [upgrade steps](operations.md#upgrade). Do not recreate its token or overwrite its configuration.

The service runs as `cairn`, stores data under `/var/lib/cairn` and listens on `127.0.0.1:8080` at first. It reads its settings from `/etc/cairn/cairn.env`, which lists every variable with a comment. Settings and author photos live in the database. Use `journalctl -u cairn` for service logs; the lines Cairn prints at start show what it is using.

## Access from your devices

The private library has no login screen. Anyone, and any program, that can reach the private listener can read and manage every page, agent key or not, so Cairn only listens on loopback or a Tailscale IP and refuses anything else. Only run agents you trust on machines that can reach it; [SECURITY.md](../SECURITY.md#security-model) explains why.

Install Tailscale on the server and your devices, sign them into your tailnet, and restrict access to this service to yourself. With the default loopback listener:

```sh
sudo tailscale serve --bg http://127.0.0.1:8080
```

Open the private HTTPS address Tailscale prints. Follow [Tailscale Serve's documentation](https://tailscale.com/docs/reference/tailscale-cli/serve) if its first setup needs approval. This gives library and management access to whoever your tailnet policy permits. Do not use Tailscale Funnel for the private listener.

Cairn answers only names it knows: IP addresses, `localhost`, single-label names such as `raspberrypi`, names under `.ts.net` (which covers `tailscale serve`) and the host of `CAIRN_PRIVATE_BASE_URL`. If you open it at another name, such as one your own DNS or proxy provides, add that name to `CAIRN_ALLOWED_HOSTS`. Any other name gets "This address isn’t allowed", which stops a website from reaching the library by pointing its own name at your server.

Alternatively, set `CAIRN_LISTEN` in `/etc/cairn/cairn.env` to the server's own Tailscale IP and a port, then restart Cairn. `tailscale ip -4` prints that IP. Cairn checks that the address belongs to the machine. It refuses names such as `localhost`, wildcard addresses such as `0.0.0.0`, and ordinary LAN or public addresses, and its message says what to use instead.

## Configure publishing

Open Settings through the private address your agents will use. Add an agent, create its key and copy the setup instructions. Store the key separately from reusable instructions. Agents running outside your tailnet need a deliberately authorised private runner; they cannot publish through the public sharing domain.

The installation token in `publisher-token` remains a compatibility credential with broader publishing authority than individual agent keys. Keep it private and normally give agents their own keys.

## Run on a Mac

There is no macOS release archive. To run Cairn on a Mac for your own use, build it with [Go](https://go.dev/dl/) 1.26.11 or later from a clone of the repository:

```sh
go build -o cairn ./cmd/cairn
./cairn init -data ./data -owner "Alex"
./cairn serve -data ./data
```

Open `http://127.0.0.1:8080`. Cairn runs while the terminal stays open and stops when you press Ctrl+C or close it; your pages stay in `./data`. To reach it from your phone, Tailscale's macOS app can run the same `tailscale serve` command shown above. For a library that is always on, run it on a Linux machine with the systemd service above.
