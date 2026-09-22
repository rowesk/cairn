# Security

Use GitHub's private vulnerability reporting on the Security tab to report a vulnerability. Do not post credentials or private report content in public issues. Supported security fixes target the latest release; there is no guaranteed response time.

The private listener has no application login. Network access to it grants owner-level library and management access. Keep it on localhost or behind owner-restricted Tailscale access. Do not expose it to the public internet or a tailnet containing untrusted members.

The public listener serves explicitly shared pages only. Short public paths are discoverable. Password protection is optional and uses one shared password. Public sharing can include imported original files as well as the rendered report. Agents may replace their own author's shared content, so grant keys only to trusted publishers.

The data directory contains private request metadata and credentials. Use a dedicated OS account, preserve backup permissions, and keep it away from untrusted local users. Cairn assumes its data directory and host are trusted. It does not provide encrypted storage, multi-user isolation or version history.

Reports can run scripts inside an opaque-origin iframe sandbox. Remote scripts, network requests, forms and access to the library document are restricted by CSP. Keep your reverse proxy's Host forwarding and no-cache settings intact. Test both listeners after changing proxy configuration.
