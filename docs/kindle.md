# Send to Kindle

Cairn can email any page to your Kindle as an EPUB book. The library's row menu and the reader (its cover, and the More menu in its bar) both have a Send to Kindle action. Only the private listener offers it; shared pages never show it.

## What reaches the Kindle

Cairn builds the book from the saved page. Text, headings, lists, tables, footnotes, links to public websites and the page's own PNG, JPEG and GIF images come through. Headings become the book's contents list. The byline and date sit under the title. Your request never leaves Cairn.

Scripts cannot run on a Kindle, so charts and diagrams drawn by scripts, embedded frames, forms and the page's own styling are left out. The book is always light, whatever Cairn's theme. Cairn stores Markdown web images when you save, so their PNG, JPEG and GIF images also come through. WebP images and web images that could not be saved are replaced by their alt text. A book can be at most about 18 MiB, which keeps the email under Gmail's 25 MB limit.

## Set up the mail account

Cairn sends through an ordinary SMTP account. Port 465 uses TLS from the start. Any other port must offer STARTTLS, unless the relay runs on the same machine.

For Gmail or Google Workspace, turn on 2-Step Verification for the sending account, then create an app password at <https://myaccount.google.com/apppasswords>. Google shows it in groups of four letters. Save it without the spaces.

On a systemd installation, put the password in a file only the service can read:

```sh
sudo install -m 0600 -o cairn -g cairn /dev/null /var/lib/cairn/smtp-password
sudo nano /var/lib/cairn/smtp-password
```

Then add the account to `/etc/cairn/cairn.env` and restart:

```sh
CAIRN_MAIL_FROM=you@example.com
CAIRN_SMTP_SERVER=smtp.gmail.com:587
CAIRN_SMTP_PASSWORD_FILE=/var/lib/cairn/smtp-password
```

```sh
sudo systemctl restart cairn
```

`CAIRN_SMTP_USERNAME` defaults to the sending address. The same settings are available as `-mail-from`, `-smtp-server`, `-smtp-username` and `-smtp-password-file` flags, and `CAIRN_SMTP_PASSWORD` can hold the password directly for local testing. Cairn refuses to start if the server has no port, the sending address is not a plain address, or the password is missing.

## Tell Amazon about the sender

Amazon throws away mail from addresses it does not know.

1. Open Manage Your Content and Devices on Amazon, then Preferences, then Personal Document Settings.
2. Copy your Kindle's Send to Kindle email address.
3. Under Approved Personal Document E-mail List, add the address in `CAIRN_MAIL_FROM`.
4. In Cairn, open Settings, paste the Kindle address under Send to Kindle and save.

## When a send fails

Cairn says why in plain words: it couldn't reach the mail server, the mail server refused its sign-in, the mail server refused the message, or the mail server asked it to try later. Try again appears only when sending again can work (the server could not be reached, or asked to wait). The log keeps the mail server's own reason; check it with `journalctl -u cairn`. A refused sign-in means the app password or username is wrong. If Cairn reports a send but nothing arrives, Amazon has usually rejected the sender: check the approved list, then look for a reply from Amazon in the sending account's inbox.
