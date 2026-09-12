"""Build-only Windows dependency staging. Does not execute installers.

python scripts/stage-nlu-runtime.py --arch amd64 --seven-zip 7z
Python and 7-Zip are contributor/build tools; DLLs and notices are embedded in
the release binary. Linux/macOS need no MSVC staging.
"""
import argparse
import hashlib
import json
from pathlib import Path
import struct
import subprocess
import sys
import tempfile

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "python"))
from fetch_intent_nlu_models import fetch, valid


def stage(arch, cache, destination, seven_zip):
    lock = json.loads((ROOT / "python/mert-runtime-sources.json").read_text())['windowsCRT']
    target = lock['targets']['windows/' + arch]
    archive = next(x for x in lock['archives'] if ('x64' if arch == 'amd64' else 'arm64') in x['path'])
    path = cache / archive['path']
    fetch(archive, path)
    data = path.read_bytes()
    cabinets, cursor = [], 0
    while (offset := data.find(b'MSCF', cursor)) >= 0:
        cursor = offset + 4
        if offset + 36 > len(data):
            continue
        size = struct.unpack_from('<I', data, offset + 8)[0]
        if 36 <= size <= len(data) - offset and data[offset+24:offset+26] == b'\x03\x01':
            cabinets.append(data[offset:offset+size])
    if len(cabinets) != 2:
        raise ValueError('Unexpected pinned Microsoft container')
    destination.mkdir(parents=True, exist_ok=True)
    with tempfile.TemporaryDirectory(prefix='playlistai-nlu-crt-') as work:
        work = Path(work)
        cabinet = work / 'payload.cab'
        cabinet.write_bytes(cabinets[1])
        subprocess.run([seven_zip, 'x', '-y', '-o'+str(work/'payload'), str(cabinet)], check=True, stdout=subprocess.DEVNULL)
        nested = work / 'payload' / ('a4' if arch == 'amd64' else 'a1')
        subprocess.run([seven_zip, 'x', '-y', '-o'+str(work/'crt'), str(nested)], check=True, stdout=subprocess.DEVNULL)
        for item in target['files']:
            source = work / 'crt' / (item['name']+'_'+arch)
            if not valid(source, item):
                raise ValueError('CRT checksum mismatch: ' + item['name'])
            dll = source.read_bytes()
            machine = struct.unpack_from('<H', dll, struct.unpack_from('<I', dll, 60)[0]+4)[0]
            if machine != {'amd64': 0x8664, 'arm64': 0xaa64}[arch]:
                raise ValueError('CRT architecture mismatch')
            (destination / (arch+'-'+item['name'])).write_bytes(dll)
    # Keep the original, checksum-pinned Microsoft license with distributed DLLs.
    for item in lock['licenseSources'][:2]:
        fetch(item, cache / item['path'])
        (destination / item['path']).write_bytes((cache/item['path']).read_bytes())
    inventory = {x['name']: x['sha256'] for x in target['files']}
    (destination/(arch+'-inventory.json')).write_text(json.dumps(inventory, indent=2)+'\n')
    print('Staged verified app-local Windows '+arch+' dependencies and licenses')


if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--arch', choices=['amd64', 'arm64'], required=True)
    parser.add_argument('--cache', type=Path, default=ROOT/'bin/nlu-runtime-sources')
    parser.add_argument('--output', type=Path, default=ROOT/'internal/nluresources/resources')
    parser.add_argument('--seven-zip', default='7z')
    args = parser.parse_args()
    stage(args.arch, args.cache, args.output, args.seven_zip)
