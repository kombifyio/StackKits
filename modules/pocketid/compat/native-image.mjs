// Existing public release-image workflow adapter. Never consumes a developer image archive.
import fs from 'node:fs';
import path from 'node:path';
import {execFileSync,spawnSync} from 'node:child_process';
import {createHash} from 'node:crypto';
import {isDeepStrictEqual} from 'node:util';
const here=import.meta.dirname, recipe=JSON.parse(fs.readFileSync(path.join(here,'build-recipe.json')));
const mode=process.argv[2], platform=process.env.POCKETID_PLATFORM;
const fail=code=>{throw Error('pocketid_native_'+code);};
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
const read=file=>JSON.parse(fs.readFileSync(file));
const save=(file,value)=>fs.writeFileSync(file,JSON.stringify(value,null,2)+'\n');
const command=(bin,args,options={})=>execFileSync(bin,args,{encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:30000,maxBuffer:8*1024*1024,...options});
const docker=(...args)=>command('docker',args).trim();
const source=command('git',['rev-parse','HEAD']).trim(),tag=process.env.TAG;
if(process.env.GITHUB_REPOSITORY?.toLowerCase()!=='kombifyio/stackkits'||
  !['release','workflow_dispatch'].includes(process.env.GITHUB_EVENT_NAME)||
  !/^v\d+\.\d+\.\d+(?:(?:-beta\.|-edge\.)\d+)?$/.test(tag??'')||
  !/^[a-f0-9]{40}$/.test(source)||source!==process.env.GITHUB_SHA||
  command('git',['rev-parse',`refs/tags/${tag}^{commit}`]).trim()!==source)fail('context_denied');
if(!recipe.platforms[platform]||process.platform!=='linux'||'linux/'+process.arch.replace('x64','amd64')!==platform)fail('native_platform_required');
const arch=platform.split('/')[1],out=path.resolve(process.env.RUNNER_TEMP,'pocketid-native-'+arch);
const reference=recipe.registry+':'+recipe.image_tag_prefix+tag+'-'+arch+'-g'+source;
const bases=recipe.platforms[platform].bases;
if(mode==='build'){
  const publisherVersion=command('skopeo',['--version']).trim();
  if(fs.existsSync(out))fail('owned_output_exists');fs.mkdirSync(out,{recursive:true});
  if(hash(fs.readFileSync(path.join(here,'Dockerfile')))!==recipe.dockerfile_sha256)fail('recipe_changed');
  command('python3',[path.join(here,'prepare.py'),'--output',path.join(out,'source')],{timeout:90000});
  const checkout=path.join(out,'source','pocket-id-'+recipe.upstream_commit);
  fs.copyFileSync(path.join(here,'Dockerfile'),path.join(checkout,'Dockerfile'));
  const builder='pocketid-native-'+process.env.GITHUB_RUN_ID+'-'+process.env.GITHUB_RUN_ATTEMPT+'-'+arch;
  if(!/^pocketid-native-\d+-\d+-(amd64|arm64)$/.test(builder))fail('run_coordinates');
  let created=false;
  try{
    docker('buildx','create','--name',builder,'--driver','docker-container','--driver-opt','image='+bases.buildkit.reference,'--driver-opt','memory=3g','--driver-opt','cpu-period=100000','--driver-opt','cpu-quota=200000');created=true;
    command('docker',['buildx','build','--builder',builder,'--platform',platform,'--provenance=mode=max','--sbom=true',
      '--build-arg','NODE_IMAGE='+bases.node.reference,'--build-arg','GO_IMAGE='+bases.go.reference,
      '--build-arg','RUNTIME_IMAGE='+bases.runtime.reference,'--build-arg','SOURCE_SHA='+source,'--build-arg','BUILD_TAGS=',
      '--metadata-file',path.join(out,'build-metadata.json'),'--output','type=oci,dest='+path.join(out,'image.oci.tar'),checkout],
      {timeout:20*60*1000,maxBuffer:16*1024*1024,stdio:'inherit'});
  }finally{if(created)docker('buildx','rm',builder);}
  save(path.join(out,'build-running.json'),{tag:'pocketid-native:'+source+'-'+arch,platform});
  command('python3',[path.join(here,'load-oci.py')],{cwd:out,env:{...process.env,POCKETID_NATIVE_EVIDENCE_ROOT:out},timeout:120000});
  save(path.join(out,'source-preflight.json'),{license:recipe.license_sha256,bases:{'postgres:18':{pinned:bases.postgres.reference,manifest:bases.postgres.reference.split('@')[1]}}});
  save(path.join(out,'source-receipt.json'),{source_sha:source,tag,platform,upstream:recipe.upstream_commit,patch_sha256:recipe.patch_sha256,dockerfile_sha256:recipe.dockerfile_sha256,reference,skopeo_version:publisherVersion,node_version:process.versions.node});
}else if(mode==='qualify'){
  const identity=read(path.join(out,'image-content.json')),qualified=read(path.join(here,'qualified-amd64.json'));
  const container='pocketid-readback-'+process.env.GITHUB_RUN_ID+'-'+arch;
  docker('create','--name',container,identity.imageId);
  try{docker('cp',container+':/app/pocket-id',path.join(out,'pocket-id'));docker('cp',container+':/usr/share/licenses/pocket-id/LICENSE',path.join(out,'LICENSE'));}
  finally{docker('rm',container);}
  const binary=hash(fs.readFileSync(path.join(out,'pocket-id'))),license=hash(fs.readFileSync(path.join(out,'LICENSE')));
  if(license!==recipe.license_sha256)fail('license_changed');
  const buildInfo=command('go',['version','-m',path.join(out,'pocket-id')],{cwd:out,env:{...process.env,GOTOOLCHAIN:'local'}});
  if(!buildInfo.includes('go1.27.1')||!buildInfo.includes('CGO_ENABLED=0')||buildInfo.includes('e2etest'))fail('binary_toolchain');
  fs.writeFileSync(path.join(out,'go-build-info.txt'),buildInfo);
  if(docker('run','--rm','--network','none','--entrypoint','/sbin/apk',identity.imageId,'info','-v','su-exec')!=='su-exec-0.3-r0')fail('runtime_apk');
  const config=JSON.parse(docker('image','inspect',identity.imageId))[0].Config;
  const runtime=Object.fromEntries(Object.keys(qualified.runtime_config).map(key=>[key,config[key]??null]));
  const reuse=arch==='amd64'&&binary===qualified.binary_sha256&&license===qualified.license_sha256&&
    recipe.patch_sha256===qualified.patch_sha256&&recipe.archive_sha256===qualified.archive_sha256&&
    bases.runtime.reference===qualified.runtime_base&&isDeepStrictEqual(runtime,qualified.runtime_config);
  if(!reuse){
    command(process.execPath,[path.join(here,'qualification/protocol.mjs')],{env:{...process.env,POCKETID_NATIVE_EVIDENCE_ROOT:out},timeout:120000});
    const proof=read(path.join(out,'protocol-receipt.json'));
    if(proof.cases.length!==9||proof.cases.some(c=>c.passed!==true)||Object.values(proof.cleanup).some(v=>v!==true)||proof.foreignContainersUnchanged!==true)fail('protocol_not_qualified');
  }
  save(path.join(out,'qualification.json'),{schema:'stackkits.pocketid-native-qualification.v1',source_sha:source,tag,platform,
    image_config:identity.imageId,binary_sha256:binary,license_sha256:license,patch_sha256:recipe.patch_sha256,
    mode:reuse?'reuse-prior-identical-embedded-binary-and-runtime-configuration':'actual-native-http-postgres-9-of-9',
    prior_manifest:reuse?qualified.qualification_manifest_sha256:null,passed_cases:9});
}else if(mode==='publish'){
  const proof=read(path.join(out,'qualification.json')),identity=read(path.join(out,'image-content.json'));
  if(proof.source_sha!==source||proof.platform!==platform||proof.tag!==tag||proof.passed_cases!==9||proof.image_config!==identity.imageId)fail('qualification_mismatch');
  const index=JSON.parse(command('tar',['-xOf',path.join(out,'image.oci.tar'),'index.json']));
  if(index.manifests.length!==1||!/^sha256:[a-f0-9]{64}$/.test(index.manifests[0].digest))fail('oci_publication_root');
  const expected=index.manifests[0].digest;
  const current=spawnSync('skopeo',['inspect','--raw','docker://'+reference],{encoding:'utf8',timeout:30000,maxBuffer:8*1024*1024});
  if(current.status===0){if('sha256:'+hash(Buffer.from(current.stdout))!==expected)fail('immutable_tag_conflict');}
  else{
    if(!/manifest unknown|manifest_unknown|name unknown/i.test(current.stderr??''))fail('registry_read_failed');
    // Source-built native OCI output only; copy all provenance/SBOM blobs unchanged.
    command('skopeo',['copy','--all','--preserve-digests','oci-archive:'+path.join(out,'image.oci.tar'),'docker://'+reference],{timeout:180000});
  }
  const actual=Buffer.from(command('skopeo',['inspect','--raw','docker://'+reference]));
  if('sha256:'+hash(actual)!==expected)fail('registry_digest_mismatch');
  save(path.join(out,'publication.json'),{...proof,schema:'stackkits.pocketid-native-publication.v1',reference,index_digest:expected,platform_manifest:identity.ociManifest,native_run:Number(process.env.GITHUB_RUN_ID),native_attempt:Number(process.env.GITHUB_RUN_ATTEMPT)});
  console.log(JSON.stringify({platform,reference,index_digest:expected}));
}else fail('mode_invalid');
