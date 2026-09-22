# Back up, restore and upgrade

One Cairn process owns a data directory. It contains SQLite metadata, report objects, imported originals, author photos and `share.key`. With the standard setup it also contains the installation publisher token. Treat all of it as private.

## Backup

For the packaged systemd installation:

```sh
sudo systemctl stop cairn
sudo install -d -m 0700 /var/backups/cairn
sudo tar -C /var/lib -cpf /var/backups/cairn/data-backup.tar cairn
sudo chmod 0600 /var/backups/cairn/data-backup.tar
sudo systemctl start cairn
```

Use a new filename for each retained backup. Also back up `/etc/cairn/cairn.env`, your service definition/overrides, proxy configuration and any credential stored outside the data directory. Keep backup permissions private from creation and copy backups off the machine to private storage. Restart the service even if a backup command fails. Never copy a live SQLite database piecemeal.

## Restore

Stop Cairn. Retain the current data directory under another name before replacing it. Extract your trusted backup under `/var/lib`, restore the original credential and configuration, and ensure `/var/lib/cairn` belongs to `cairn:cairn` with directory mode 0700. Start Cairn, then check private reading, an image, an original download and public sharing/password behaviour if enabled.

Keep `share.key` with its database. Test restoration to a separate directory and loopback port periodically. Do not point two Cairn processes at the same data directory.

## Upgrade

Download the release for your architecture, verify its checksum and read its release notes. Take a stopped-service backup first. Keep the current binary and configuration. With Cairn stopped, install the new binary at `/usr/local/bin/cairn`, then start the service.

Do not rerun the first-install commands or replace your token, environment file or data directory. `cairn version` reports the binary version. Check existing reports, Settings, publishing with a disposable page and public revocation before considering the upgrade complete.

## Rollback

Stop Cairn and reinstall the retained binary. Database compatibility is release-specific. Follow the release notes before running an older binary against newer data. If restoration is necessary, preserve the current data directory first; restoring an old backup discards changes made since that backup.

The initial public v0.1.0 release preserves the existing personal-installation schema and credentials. The owner name is only used when creating a new preferences row; existing author settings and URLs are retained.
