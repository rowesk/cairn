#!/usr/bin/env bash
# Run only on a fresh disposable Linux host, as root.
set -euo pipefail
release=$(cd "${1:?release directory required}" && pwd)
if [[ $(id -u) != 0 ]] || id cairn >/dev/null 2>&1; then
  echo 'Requires root on a fresh host without a cairn user.' >&2
  exit 1
fi
for existing in /var/lib/cairn /etc/cairn /usr/local/bin/cairn /etc/systemd/system/cairn.service; do
  if [[ -e "$existing" ]]; then echo "Refusing to replace $existing" >&2; exit 1; fi
done
cleanup() {
  systemctl stop cairn 2>/dev/null || true
  rm -f /etc/systemd/system/cairn.service /usr/local/bin/cairn
  rm -rf /var/lib/cairn /etc/cairn
  userdel cairn 2>/dev/null || true
  systemctl daemon-reload
}
trap cleanup EXIT
useradd --system --home-dir /var/lib/cairn --shell /usr/sbin/nologin cairn
install -d -m 0700 -o cairn -g cairn /var/lib/cairn
install -m 0755 "$release/cairn" /usr/local/bin/cairn
runuser -u cairn -- /usr/local/bin/cairn init -data /var/lib/cairn -owner 'Service test owner' | grep 'Created a Cairn library' >/dev/null
/usr/local/bin/cairn help | grep CAIRN_LISTEN >/dev/null
install -d -m 0755 /etc/cairn
install -m 0644 "$release/deploy/cairn.env" /etc/cairn/cairn.env
install -m 0644 "$release/deploy/cairn.service" /etc/systemd/system/cairn.service
systemctl daemon-reload
systemctl start cairn
curl --retry 10 --retry-connrefused --retry-delay 1 -fsS http://127.0.0.1:8080/healthz | grep '"status":"ok"' >/dev/null
curl -fsS http://127.0.0.1:8080/settings | grep 'Service test owner' >/dev/null
systemctl is-active --quiet cairn
banner() {
  for _ in $(seq 10); do
    journalctl -u cairn --no-pager | grep -F "$1" >/dev/null && return 0
    sleep 1
  done
  echo "Missing startup line: $1" >&2
  return 1
}
banner 'Cairn private listener: 127.0.0.1:8080'
banner 'Sharing: off'
# The unit passes only -data and -publisher-token-file, so these prove Cairn reads its environment file itself.
cat >> /etc/cairn/cairn.env <<'CONFIG'
CAIRN_LISTEN=127.0.0.1:8090
CAIRN_PUBLIC_LISTEN=127.0.0.1:8081
CAIRN_PUBLIC_BASE_URL=https://reports.example.com
CONFIG
systemctl restart cairn
curl --retry 10 --retry-connrefused --retry-delay 1 -fsS http://127.0.0.1:8090/healthz | grep '"database":"ok"' >/dev/null
curl -fsS http://127.0.0.1:8081/healthz | grep -x '{"status":"ok"}' >/dev/null
[[ $(curl -s -o /dev/null -w '%{http_code}' http://127.0.0.1:8081/settings) == 404 ]]
banner 'Sharing: on, public links start https://reports.example.com'
echo 'Packaged systemd service passed private-only startup, environment settings and optional public configuration.'
