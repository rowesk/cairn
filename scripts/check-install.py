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
def start(directory, token):
    process = subprocess.Popen([binary, '-data', str(directory), '-publisher-token-file', str(token), '-listen', '127.0.0.1:0', '-public-listen', '127.0.0.1:0', '-public-base-url', 'https://reports.example.com'], stderr=subprocess.PIPE, text=True)
    addresses = {}
    for _ in range(10):
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

with tempfile.TemporaryDirectory(prefix='cairn-install-') as temporary:
    directory = pathlib.Path(temporary) / 'data'
    token = directory / 'publisher-token'
    subprocess.run([binary, 'init', '-data', str(directory), '-owner', 'Alex Example'], check=True, stdout=subprocess.DEVNULL)
    credential = token.read_bytes()
    assert token.stat().st_mode & 0o777 == 0o600
    subprocess.run([binary, 'init', '-data', str(directory), '-owner', 'Do not overwrite'], check=True, stdout=subprocess.DEVNULL)
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
    process, private, public = start(restored, restored / 'publisher-token')
    try:
        assert request(private, route)[0] == 200
        assert request(private, '/_content' + route + '?asset=image.png')[0] == 200
        assert request(public, route)[0] == 404
        assert form(private, route[1:], 'share') == 303
        assert request(public, route)[0] == 200
    finally:
        stop(process)
print('Binary install check passed: setup, preserved credentials/owner, publication, sharing, revocation, restart and restored data.')
