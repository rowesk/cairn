#!/usr/bin/env python3
"""Collect notices from the pinned local Go and npm dependency installations."""
import json
import pathlib
import subprocess

root = pathlib.Path(__file__).resolve().parents[1]
output = root / 'THIRD_PARTY_NOTICES.md'
sections = ['# Third-party notices\n\nCairn is MIT licensed. Dependencies retain the following notices. This collection includes the Go runtime, modules used by the binary, bundled browser dependencies and npm development dependencies. Build tools are included conservatively; listing them does not mean they run in the installed service.\n']
def include(label, files):
    sections.append('\n## ' + label + '\n')
    for file in files:
        sections.append('\n### ' + file.name + '\n\n```text\n' + file.read_text(errors='replace').strip() + '\n```\n')
def notices(directory):
    return sorted(p for p in directory.iterdir() if p.is_file() and p.name.lower().startswith(('license', 'copying', 'notice')))

goroot = pathlib.Path(subprocess.check_output(['go', 'env', 'GOROOT'], cwd=root, text=True).strip())
go_license = goroot / 'LICENSE'
if not go_license.exists():
    go_license = goroot.parent / 'LICENSE'
include('Go runtime', [go_license])
raw = subprocess.check_output(['go', 'list', '-deps', '-json', './cmd/cairn'], cwd=root, text=True)
decoder = json.JSONDecoder()
modules = {}
while raw.strip():
    obj, end = decoder.raw_decode(raw.lstrip())
    raw = raw.lstrip()[end:]
    module = obj.get('Module')
    if module and not module.get('Main'):
        modules[module['Path']] = module
for name, module in sorted(modules.items()):
    files = notices(pathlib.Path(module['Dir']))
    if not files:
        raise SystemExit('Missing module notice: ' + name)
    include(name + ' ' + module['Version'], files)
lock = json.loads((root / 'package-lock.json').read_text())
for relative, package in sorted(lock['packages'].items()):
    if not relative:
        continue
    directory = root / relative
    if not directory.exists() and package.get('optional'):
        continue
    files = notices(directory)
    for subdir in ['licenses', 'LICENSES']:
        if (directory / subdir).is_dir():
            files += sorted(p for p in (directory / subdir).rglob('*') if p.is_file())
    name = relative.split('node_modules/')[-1]
    if files:
        include(name + ' ' + package['version'], files)
    elif (directory / 'README.md').exists() and 'Permission is hereby granted' in (directory / 'README.md').read_text():
        text = (directory / 'README.md').read_text()
        begin = text.rfind('Copyright', 0, text.index('Permission is hereby granted'))
        if begin < 0:
            raise SystemExit('Missing copyright: ' + name)
        sections.append('\n## ' + name + ' ' + package['version'] + '\n\nNotice from package README:\n\n```text\n' + text[begin:].strip() + '\n```\n')
    else:
        raise SystemExit('Missing npm licence: ' + name)
include('DM Sans font', [root / 'internal/cairn/ui/OFL.txt'])
output.write_text(''.join(sections))
