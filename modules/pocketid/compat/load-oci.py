"""Load the verified native-platform OCI image into this host's legacy Docker store.

Preserves exact config and uncompressed layer digests; keeps OCI provenance.
This is an archive-format adapter, not a rebuild or a replacement image.
"""
import gzip, hashlib, io, json, subprocess, tarfile, os
from pathlib import Path
root=Path(os.environ['POCKETID_NATIVE_EVIDENCE_ROOT']).resolve()
receipt=json.loads((root/'build-running.json').read_text())
arch=receipt['platform'].split('/')[1]
def sha(data): return hashlib.sha256(data).hexdigest()
with tarfile.open(root/'image.oci.tar','r') as src:
    def blob(digest):
        data=src.extractfile('blobs/'+digest.replace(':','/')).read()
        if 'sha256:'+sha(data)!=digest: raise ValueError('OCI content digest mismatch')
        return data
    index=json.load(src.extractfile('index.json'))
    descriptor=index['manifests'][0]
    obj=json.loads(blob(descriptor['digest']))
    if 'manifests' in obj:
        descriptor=next(m for m in obj['manifests'] if m.get('platform',{}).get('architecture')==arch and m['platform']['os']=='linux')
        obj=json.loads(blob(descriptor['digest']))
    config=blob(obj['config']['digest']); conf=json.loads(config)
    assert conf['architecture']==arch and conf['os']=='linux'
    config_name=sha(config)+'.json'; layers=[]
    with tarfile.open(root/'image.docker.tar','w') as dst:
        def add(name,data):
            info=tarfile.TarInfo(name);info.size=len(data);info.mtime=0;dst.addfile(info,io.BytesIO(data))
        add(config_name,config)
        for i, layer in enumerate(obj['layers']):
            data=blob(layer['digest'])
            if layer['mediaType'].endswith('+gzip'):data=gzip.decompress(data)
            elif layer['mediaType'].endswith('+zstd'):raise ValueError('unsupported compression')
            assert 'sha256:'+sha(data)==conf['rootfs']['diff_ids'][i]
            name=f'{i}/layer.tar';layers.append(name);add(name,data)
        add('manifest.json',json.dumps([{'Config':config_name,'RepoTags':[receipt['tag']],'Layers':layers}]).encode())
    result={'ociManifest':descriptor['digest'],'imageId':obj['config']['digest'],'tag':receipt['tag'],'layerDiffIds':conf['rootfs']['diff_ids'],'ociArchiveSHA256':sha((root/'image.oci.tar').read_bytes()),'dockerArchiveSHA256':sha((root/'image.docker.tar').read_bytes())}
subprocess.run(['docker','load','--input',str(root/'image.docker.tar')],check=True,capture_output=True)
actual=json.loads(subprocess.check_output(['docker','image','inspect',receipt['tag']],text=True))
assert actual[0]['Id']==result['imageId']
assert actual[0]['RootFS']['Layers']==result['layerDiffIds']
(root/'image-inspect.json').write_text(json.dumps(actual,indent=2))
(root/'image-content.json').write_text(json.dumps(result,indent=2))
print(json.dumps({k:result[k] for k in ['imageId','ociManifest','tag']}))
