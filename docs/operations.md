# Back up, restore, upgrade and remove

One Cairn process owns a data directory. It contains SQLite metadata, report objects, imported originals, author photos, `share.key` and `form.key`. With the standard setup it also contains the installation installation token. Treat all of it as private.

`share.key` signs the unlock cookies of password-protected pages. `form.key` signs the forms in your library, so a form left open across a restart still saves. Cairn creates both on first start with owner-only permissions.

## Backup

For the packaged systemd installation:

```sh
sudo systemctl stop cairn
sudo install -d -m 0700 /var/backups/cairn
sudo tar -C /var/lib -cpf /var/backups/cairn/data-backup.tar cairn
sudo chmod 0600 /var/backups/cairn/data-backup.tar
sudo systemctl start cairn
```

Use a new filename for each retained backup. Also back up `/etc/cairn/cairn.env`, your service definition and overrides, proxy configuration and any credential stored outside the data directory, such as the mail password file. Keep backup permissions private from creation and copy backups off the machine to private storage. Restart the service even if a backup command fails. Never copy a live SQLite database piecemeal.

## Restore

Stop Cairn. Keep the current data directory under another name before replacing it. Extract your trusted backup under `/var/lib`, restore the original token and configuration, and make sure `/var/lib/cairn` belongs to `cairn:cairn` with directory mode 0700. Start Cairn, then check private reading, an image, an original download, and public sharing and passwords if you use them.

Keep `share.key` and `form.key` with their database. Restoring without `share.key` asks visitors to enter page passwords again. Restoring without `form.key` makes Cairn create a new one, and any library form still open from before asks to be submitted again; nothing else is lost. Test restoring to a separate directory from time to time, and start that copy on its own loopback port with `-data` and `-listen 127.0.0.1:8090` so it runs beside the service. Never point two Cairn processes at the same data directory. Cairn refuses to start if another process holds it.

## Upgrade

Read the release notes first. Then, from a directory holding the new archive and `checksums.txt`:

```sh
sha256sum --ignore-missing --check checksums.txt
tar -xzf cairn_vX.Y.Z_linux_arm64.tar.gz
sudo systemctl stop cairn
sudo install -d -m 0700 /var/backups/cairn
sudo tar -C /var/lib -cpf /var/backups/cairn/before-vX.Y.Z.tar cairn
sudo cp -p /usr/local/bin/cairn /usr/local/bin/cairn.previous
sudo install -m 0755 cairn_vX.Y.Z_linux_arm64/cairn /usr/local/bin/cairn
sudo systemctl start cairn
sudo journalctl -u cairn -n 10 --no-pager
```

Replace `vX.Y.Z` and `arm64` with the release and architecture you downloaded. The last command shows the lines Cairn printed at start: check that the version is the new one and that sharing and Send to Kindle are on or off as before.

Replace only the binary. Do not rerun `init` or the first-install commands, and do not replace your token, environment file, service file or data directory. Your existing service file keeps working with newer releases. Compare it with the release's `deploy/cairn.service` only when the release notes ask you to. Check existing reports, Settings, publishing with a disposable page and public revocation before you consider the upgrade complete.

## Rollback

Stop Cairn and put back the previous binary:

```sh
sudo systemctl stop cairn
sudo install -m 0755 /usr/local/bin/cairn.previous /usr/local/bin/cairn
sudo systemctl start cairn
```

If you also replaced the service file, restore the old one and run `sudo systemctl daemon-reload` before starting. Database compatibility is release-specific, so follow the release notes before running an older binary against newer data. If you need to restore a backup, keep the current data directory first. Restoring an old backup discards changes made since that backup.

`init` only uses `-owner` when it creates a library. Rerunning it never changes an existing author, token or URL, and it says so.

## Remove Cairn

Back up first if you might want the pages again. Then stop and remove the service, its configuration, its data and its user:

```sh
sudo systemctl disable --now cairn
sudo rm /etc/systemd/system/cairn.service
sudo systemctl daemon-reload
sudo rm -r /etc/cairn /var/lib/cairn
sudo rm -f /usr/local/bin/cairn /usr/local/bin/cairn.previous
sudo userdel cairn
```

`rm -r /var/lib/cairn` deletes every page and the installation token for good. If you put Cairn on your tailnet with `tailscale serve`, run `tailscale serve status` and remove that entry; `sudo tailscale serve reset` clears every Serve entry on the machine, so use it only if Cairn was the only one. Remove any proxy, tunnel or DNS record you set up for public sharing, and revoke the mail app password if you set up Send to Kindle.

To remove a copy you ran by hand, stop it with Ctrl+C and delete the binary and its data directory.
