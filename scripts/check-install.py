#!/usr/bin/env python3
"""Exercise a downloaded binary with new data, reinitialization and restore."""
import base64
import json
import os
import pathlib
import re
import shutil
import subprocess
import sys
import tempfile
import urllib.error
import urllib.parse
import urllib.request

binary = str(pathlib.Path(sys.argv[1]).resolve())
class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None
client = urllib.request.build_opener(NoRedirect)
def request(base, path, data=None, headers=None):
    req = urllib.request.Request(base + path, data=data, headers=headers or {})
    try:
        response = client.open(req, timeout=10)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        return response.status, response.headers, response.read()
def start(directory, token=None):
    """Start with flags, or with token=None from CAIRN_* variables and the data directory's own token."""
    if token:
        process = subprocess.Popen([binary, '-data', str(directory), '-publisher-token-file', str(token), '-listen', '127.0.0.1:0', '-public-listen', '127.0.0.1:0', '-public-base-url', 'https://reports.example.com'], stderr=subprocess.PIPE, text=True)
    else:
        environment = {key: value for key, value in os.environ.items() if not key.startswith('CAIRN_')}
        environment.update(CAIRN_DATA=str(directory), CAIRN_LISTEN='127.0.0.1:0', CAIRN_PUBLIC_LISTEN='127.0.0.1:0', CAIRN_PUBLIC_BASE_URL='https://reports.example.com')
        process = subprocess.Popen([binary, 'serve'], stderr=subprocess.PIPE, text=True, env=environment)
    addresses = {}
    for _ in range(20):
        line = process.stderr.readline()
        if not line:
            raise RuntimeError('service did not start')
        match = re.search(r'Cairn (private|public) listener: (\S+)', line)
        if match:
            addresses[match[1]] = 'http://' + match[2]
        if len(addresses) == 2:
            return process, addresses['private'], addresses['public']
    process.terminate()
    process.wait(timeout=10)
    raise RuntimeError('missing listeners')
def stop(process):
    process.terminate()
    process.wait(timeout=15)
def form(base, slug, action):
    path = '/manage/' + slug
    status, _, body = request(base, path)
    assert status == 200
    csrf = re.search(rb'name="csrf" value="([^"]+)"', body).group(1).decode()
    return request(base, path, urllib.parse.urlencode({'csrf': csrf, 'action': action}).encode(), {'Content-Type': 'application/x-www-form-urlencoded', 'Origin': base})[0]

def cli(*args, cwd=None, env=None):
    environment = {key: value for key, value in os.environ.items() if not key.startswith('CAIRN_')}
    environment.update(env or {})
    return subprocess.run([binary, *args], capture_output=True, text=True, cwd=cwd, env=environment, timeout=30)

with tempfile.TemporaryDirectory(prefix='cairn-install-') as temporary:
    for args in [['help'], ['-h'], ['init', '-h'], ['help', 'serve']]:
        result = cli(*args)
        assert result.returncode == 0 and 'CAIRN_DATA' in result.stdout and not result.stderr, (args, result)
    result = cli('help', 'publish')
    assert result.returncode == 0 and 'CAIRN_API_KEY' in result.stdout and 'CAIRN_URL' in result.stdout and not result.stderr, result
    result = cli('publish')
    assert result.returncode == 2 and result.stderr.startswith('Name the file to publish'), result
    result = cli('version')
    assert result.returncode == 0 and result.stdout.startswith('cairn '), result
    result = cli('bogus')
    assert result.returncode == 2 and result.stderr.startswith('Unknown command "bogus".') and not result.stdout, result
    result = cli('serve', '-data', './data', 'extra')
    assert result.returncode == 2 and 'Unexpected argument "extra"' in result.stderr, result
    result = cli(cwd=temporary)
    assert result.returncode == 1 and 'init -data ./data' in result.stderr, result
    assert not (pathlib.Path(temporary) / 'data').exists(), 'a refused start created the data directory'
    directory = pathlib.Path(temporary) / 'data'
    token = directory / 'publisher-token'
    result = cli('init', '-data', str(directory), '-owner', 'Alex Example')
    assert result.returncode == 0 and result.stdout.startswith('Created a Cairn library') and 'serve -data' in result.stdout, result
    credential = token.read_bytes()
    assert token.stat().st_mode & 0o777 == 0o600
    result = cli('init', '-data', str(directory), '-owner', 'Do not overwrite')
    assert result.returncode == 0 and 'already set up' in result.stdout and 'was ignored' in result.stdout, result
    assert token.read_bytes() == credential
    process, private, public = start(directory, token)
    try:
        status, _, settings = request(private, '/settings')
        assert status == 200 and b'Alex Example' in settings and b'Do not overwrite' not in settings
        page = {'title': 'Install check', 'agent': 'ExampleAgent', 'original_request': 'PRIVATE-INSTALL-CONTEXT', 'markdown': '# Installation works\n\n![Image](?asset=image.png)', 'assets': {'image.png': 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII='}}
        status, _, body = request(private, '/api/pages', json.dumps(page).encode(), {'Content-Type': 'application/json', 'Authorization': 'Bearer ' + credential.decode().strip()})
        assert status == 201, (status, body)
        route = json.loads(body)['url']
        assert re.fullmatch(r'/[A-Za-z0-9]{3}', route)
        # Health on both listeners: details privately, status alone in public.
        status, _, body = request(private, '/healthz')
        health = json.loads(body)
        assert status == 200 and health['status'] == 'ok' and health['database'] == 'ok' and health['storage'] == 'ok' and health['version'], body
        status, _, body = request(public, '/healthz')
        assert status == 200 and json.loads(body) == {'status': 'ok'}, body
        # cairn publish sends a file and prints the page's full address.
        note = pathlib.Path(temporary) / 'note.md'
        note.write_text('# Published from the command line\n\nIt works.\n')
        result = cli('publish', str(note), '-request', 'PRIVATE-CLI-REQUEST', '-agent', 'Install agent', env={'CAIRN_URL': private, 'CAIRN_API_KEY': credential.decode().strip()})
        assert result.returncode == 0 and re.fullmatch(re.escape(private) + r'/[A-Za-z0-9]{3}\n', result.stdout), result
        assert request(private, result.stdout.strip()[len(private):])[0] == 200
        result = cli('publish', str(note), '-agent', 'Install agent', env={'CAIRN_URL': private, 'CAIRN_API_KEY': 'cairn_wrong'})
        assert result.returncode == 1 and 'CAIRN_API_KEY' in result.stderr, result
        assert request(public, route)[0] == 404
        assert form(private, route[1:], 'share') == 303
        for path in [route, '/_content' + route, '/_content' + route + '?asset=image.png']:
            status, _, body = request(public, path)
            assert status == 200 and b'PRIVATE-INSTALL-CONTEXT' not in body
        assert form(private, route[1:], 'unshare') == 303
        assert request(public, route)[0] == 404
        assert request(private, route)[0] == 200
    finally:
        stop(process)
    restored = pathlib.Path(temporary) / 'restored'
    shutil.copytree(directory, restored)
    process, private, public = start(restored)
    try:
        assert request(private, route)[0] == 200
        assert request(private, '/_content' + route + '?asset=image.png')[0] == 200
        assert request(public, route)[0] == 404
        assert form(private, route[1:], 'share') == 303
        assert request(public, route)[0] == 200
    finally:
        stop(process)
print('Binary install check passed: command line, setup, preserved credentials/owner, publication, cairn publish, health on both listeners, sharing, revocation, restart and restored data started from CAIRN_* settings.')
