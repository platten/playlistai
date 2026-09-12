"""Optionally embed verified MiniLM/DistilBERT assets in a release executable.

The normal setup path downloads missing assets. Running this before a platform
build makes those model files available offline from that release binary.
"""
import argparse
from pathlib import Path
import json
import shutil
import sys
import platform
import tarfile
import zipfile
import hashlib

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT / "python"))
from fetch_intent_nlu_models import fetch

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--cache', type=Path, default=Path.home()/'Downloads/playlistai-intent-nlu-v1')
    parser.add_argument('--output', type=Path, default=ROOT/'internal/nluresources/resources')
    machine = {'x86_64':'amd64','AMD64':'amd64','aarch64':'arm64','arm64':'arm64'}.get(platform.machine(),platform.machine())
    parser.add_argument('--platform', default={'Windows':'windows','Darwin':'darwin','Linux':'linux'}[platform.system()]+'/'+machine)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=True)
    for item in json.loads((ROOT/'internal/intent/nlu/sources.json').read_text()):
        if not item['setup']:
            continue
        source = args.cache/item['model']/item['name']
        fetch(item, source)
        name = item['name'].replace('/', '_')
        if item['name'] == 'onnx/model.onnx':
            name = 'model.onnx'
        shutil.copyfile(source, args.output/(item['model']+'-'+name))
    lock = json.loads((ROOT/'python/mert-runtime-sources.json').read_text())
    rt = lock['targets'][args.platform]
    base = 'onnxruntime-'+rt['target']+'-'+lock['version']
    archive = args.cache/'runtime'/(base+rt['extension'])
    fetch({'url':'https://github.com/microsoft/onnxruntime/releases/download/v'+lock['version']+'/'+archive.name, 'size':rt['size'],'sha256':rt['sha256']},archive)
    member = base+'/lib/'+rt['library']
    if rt['extension'] == '.zip':
        with zipfile.ZipFile(archive) as src:
            data = src.read(member)
            notices = {name:src.read(base+'/'+name) for name in ['LICENSE','ThirdPartyNotices.txt']}
    else:
        with tarfile.open(archive) as src:
            members = {m.name.lstrip('./'):m for m in src.getmembers()}
            if not members[member].isfile():
                raise ValueError('Runtime library must be a regular file')
            data = src.extractfile(members[member]).read(rt['librarySize']+1)
            notices = {name:src.extractfile(members[base+'/'+name]).read(4 << 20) for name in ['LICENSE','ThirdPartyNotices.txt']}
    if len(data) != rt['librarySize'] or hashlib.sha256(data).hexdigest() != rt['librarySha256']:
        raise ValueError('Runtime library checksum mismatch')
    (args.output/('runtime-'+args.platform.split('/')[1]+'-'+rt['library'])).write_bytes(data)
    for name, notice in notices.items():
        (args.output/('ONNX-'+name+'.txt')).write_bytes(notice)
    print('Staged verified intent encoders for an offline-capable release binary')
