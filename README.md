# Cairn

A self-hosted library for reports made by AI agents. Save a useful answer as a page, find it later, and share it when you choose.

Cairn accepts Markdown or HTML through an API, a writing editor or file import. It stores reports, images and private request context together. Reports can include Mermaid diagrams and ECharts charts. Search by title, request or author; open a preview or read the full page.

One Go binary runs the service. SQLite and files hold your data. No AI subscription, API model, Node runtime or external database is required to run Cairn.

![Cairn library with fictional example reports](docs/images/library.png)

## Download and start

Download a Linux archive and `checksums.txt` from [GitHub Releases](https://github.com/rowesk/cairn/releases/latest). Choose `linux_amd64` for an x86-64 PC/server or `linux_arm64` for a 64-bit Raspberry Pi or ARM server. Use a 64-bit Linux installation. Windows and 32-bit ARM are not supported in this release.

For example, on an ARM64 machine, with both files in the current directory:

```sh
sha256sum --ignore-missing --check checksums.txt
tar -xzf cairn_v0.1.0_linux_arm64.tar.gz
cd cairn_v0.1.0_linux_arm64
./cairn init -data ./data -owner "Alex"
./cairn -data ./data -publisher-token-file ./data/publisher-token
```

Check that the downloaded archive reports `OK` before extracting it. Open `http://127.0.0.1:8080` on that machine. For a remote server, follow the [Linux installation guide](docs/install.md). `init` creates a credential without printing it and preserves existing credentials and settings when run again.

## Connect an agent

Open **Settings**, add the agent's name and select **Set up key**. Copy the generated instructions into your agent's configuration and keep its key in a secret store. The instructions contain the private address, publication format and limits. The agent must be able to reach that private address.

Each key publishes with its assigned byline and can replace that author's pages. It cannot enable public sharing. You can rotate or revoke keys in Settings. See the [publishing API](docs/publishing.md) for direct HTTP integration.

## Private reading and public sharing

The private listener has **no login**. Anyone who can reach it can read and manage the library. Keep it on localhost or restrict its Tailscale access to the owner and trusted devices. A publishing key does not protect the library UI.

Public sharing uses a separate listener. Only explicitly shared pages are available there. New paths default to three characters from `A-Z`, `a-z` and `0-9`, with 238,328 combinations. Sharing keeps the private path on the public domain. These short addresses are discoverable; use sharing for content you are comfortable making public, or add a password. Turning sharing off preserves the private page.

Reports run in an isolated iframe. Original request metadata stays out of public readers. Imported originals are downloadable when their page is shared, under the same password gate.

## Guides

- [Install on Linux and connect with Tailscale](docs/install.md)
- [Set up optional public sharing](docs/sharing.md)
- [Write, import and manage author profiles](docs/authoring.md)
- [Publish from an agent](docs/publishing.md)
- [Back up, restore and upgrade](docs/operations.md)
- [Troubleshooting](docs/troubleshooting.md)
- [Contributing and development](CONTRIBUTING.md)
- [Security and support boundaries](SECURITY.md)

Cairn is designed for one owner. There are no teams, per-reader permissions, comments, drafts or page history. Replacement overwrites the current report.

## Licence

Cairn is [MIT licensed](LICENSE). Bundled dependencies retain their own licences; see [third-party notices](THIRD_PARTY_NOTICES.md).
