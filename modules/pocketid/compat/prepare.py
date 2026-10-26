#!/usr/bin/env python3
"""Verify/apply the reviewed compatibility patch; never invokes Git or Docker.

Source reconstruction is pinned. Image/toolchain digests and deployment remain
separate release responsibilities. --verify-only does not extract a checkout.
"""
import argparse, hashlib, io, json, re, tarfile, urllib.request
from pathlib import Path

def sha(data): return hashlib.sha256(data).hexdigest()

def reconstruct(old, section):
    old = old.decode('utf8').splitlines(keepends=True)
    out, pos, active = [], 0, False
    for line in section.splitlines(keepends=True):
        match = re.match(r'^@@ -(\d+)(?:,\d+)? \+\d+(?:,\d+)? @@', line)
        if match:
            start = max(0, int(match.group(1))-1)
            if start < pos: raise ValueError('Overlapping patch hunks')
            out.extend(old[pos:start]); pos = start; active = True; continue
        if not active: continue
        if line.startswith((' ', '-')):
            if pos >= len(old) or old[pos] != line[1:]: raise ValueError('Patch context mismatch')
            if line[0] == ' ': out.append(old[pos])
            pos += 1
        elif line.startswith('+'): out.append(line[1:])
        else: raise ValueError('Unsupported patch line')
    out.extend(old[pos:])
    return ''.join(out).encode('utf8')

def main():
    ap=argparse.ArgumentParser();ap.add_argument('--archive',type=Path);ap.add_argument('--output',type=Path);ap.add_argument('--verify-only',action='store_true');args=ap.parse_args()
    here=Path(__file__).resolve().parent;manifest=json.loads((here/'provenance.json').read_text());patch=(here/'membership-cas.patch').read_bytes()
    if sha(patch)!=manifest['patchSHA256']: raise ValueError('Patch digest mismatch')
    archive=args.archive.read_bytes() if args.archive else urllib.request.urlopen(manifest['archiveURL'],timeout=60).read()
    if sha(archive)!=manifest['archiveSHA256']: raise ValueError('Upstream archive digest mismatch')
    sections={}
    for part in patch.decode('utf8').split('diff --git ')[1:]:
        path=part.splitlines()[0].split(' b/',1)[1];sections[path]=part
    with tarfile.open(fileobj=io.BytesIO(archive),mode='r:gz') as source:
        prefix='pocket-id-'+manifest['upstreamCommit']+'/'
        result={}
        for entry in manifest['files']:
            path=entry['path']
            try: member=source.getmember(prefix+path);old=source.extractfile(member).read()
            except KeyError: old=b''
            if sha(old)!=entry['beforeSHA256']: raise ValueError('Upstream file mismatch: '+path)
            new=reconstruct(old,sections[path])
            if sha(new)!=entry['afterSHA256']: raise ValueError('Reconstruction mismatch: '+path)
            result[path]=new
        if set(sections)!=set(result): raise ValueError('Unexpected patch files')
        if not args.verify_only:
            if args.output is None: raise ValueError('--output is required')
            out=args.output.resolve()
            if out.exists(): raise ValueError('Output must not already exist')
            out.mkdir(parents=True)
            # Python's data filter rejects traversal and links outside the tree.
            source.extractall(out,filter='data')
            checkout=out/prefix.rstrip('/')
            for path,data in result.items():
                target=checkout/path;target.parent.mkdir(parents=True,exist_ok=True);target.write_bytes(data)
            print(checkout)
    print(json.dumps({'status':'verified','upstreamCommit':manifest['upstreamCommit'],'files':len(result),'patchSHA256':sha(patch)}))
if __name__=='__main__': main()
