// Existing public release-image workflow adapter. Never consumes a developer image archive.
import fs from 'node:fs';
import path from 'node:path';
import {execFileSync,spawnSync,spawn} from 'node:child_process';
import {createHash} from 'node:crypto';
import {isDeepStrictEqual} from 'node:util';
const here=import.meta.dirname, recipe=JSON.parse(fs.readFileSync(path.join(here,'build-recipe.json')));
const mode=process.argv[2], platform=process.env.POCKETID_PLATFORM;
const fail=code=>{throw Error('pocketid_native_'+code);};
const hash=bytes=>createHash('sha256').update(bytes).digest('hex');
const read=file=>JSON.parse(fs.readFileSync(file));
const save=(file,value)=>fs.writeFileSync(file,JSON.stringify(value,null,2)+'\n');
const command=(bin,args,options={})=>execFileSync(bin,bin==='docker'&&nativeDockerContext?['--context',nativeDockerContext,...args]:args,{encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:30000,maxBuffer:8*1024*1024,...options});
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

// Hard bounds apply to the owned Docker CLI process group, including its
// Buildx plugin. A timeout is unknown until exact resource readback completes.
let nativeDockerContext=null,nativeDockerDaemon=null;
const nativeDockerEndpoint='unix:///var/run/docker.sock';
const nativeDriverOptions=()=>({image:bases.buildkit.reference,memory:'3g','cpu-period':'100000','cpu-quota':'200000'});
async function boundedDocker(args,limit) {
  return new Promise(resolve=>{
    const child=spawn('docker',[...(nativeDockerContext?['--context',nativeDockerContext]:[]),...args],{detached:true,stdio:['ignore','pipe','pipe']});
    let stdout='',stderr='',status=null,signal=null,error=null,timedOut=false,finishing=false,bytes=0;
    const exists=()=>{if(!child.pid)return false;try{process.kill(-child.pid,0);return true;}catch(e){return e.code!=='ESRCH';}};
    const kill=()=>{if(child.pid)try{process.kill(-child.pid,'SIGKILL');}catch(e){if(e.code!=='ESRCH')error??={code:'PROCESS_GROUP_KILL_FAILED'};}};
    const finish=async()=>{
      if(finishing)return;finishing=true;clearTimeout(timer);clearTimeout(forced);
      if(exists())kill();
      const until=Date.now()+2000;while(exists()&&Date.now()<until)await new Promise(r=>setTimeout(r,25));
      const absent=!exists();if(!absent)error??={code:'PROCESS_GROUP_RETAINED'};
      child.stdout?.destroy();child.stderr?.destroy();child.unref();
      resolve({status,signal,error,stdout,stderr,timed_out:timedOut,process_group_absent:absent});
    };
    let forced;
    const timer=setTimeout(()=>{timedOut=true;error??={code:'ETIMEDOUT'};kill();forced=setTimeout(finish,250);},limit);
    const collect=(kind,chunk)=>{bytes+=chunk.length;if(bytes>1024*1024){error??={code:'OUTPUT_LIMIT'};kill();forced??=setTimeout(finish,250);return;}if(kind==='stdout')stdout+=chunk;else stderr+=chunk;};
    child.stdout.on('data',b=>collect('stdout',b));child.stderr.on('data',b=>collect('stderr',b));
    child.once('error',e=>{error={code:e.code};});child.once('exit',(code,sig)=>{status=code;signal=sig;});child.once('close',finish);
  });
}
const successful=r=>r.status===0&&!r.error&&!r.timed_out&&r.process_group_absent===true;
const jsonLines=raw=>raw.trim()?raw.trim().split(/\r?\n/).map(line=>JSON.parse(line)):[];
async function observeNativeBuilder(builder,run) {
  const container='buildx_buildkit_'+builder+'0';
  const daemon=await run(['info','--format','{{json .ID}}'],10000);
  if(!successful(daemon)||JSON.parse(daemon.stdout)!==nativeDockerDaemon)fail('cleanup_daemon_identity_unverified');
  const listed=await run(['buildx','ls','--format','json','--no-trunc','--timeout','5s'],10000);
  if(!successful(listed))fail('cleanup_registration_inventory_unverified');
  const builders=jsonLines(listed.stdout);
  if(!builders.length||builders.some(row=>typeof row.Name!=='string'||!row.Name)||new Set(builders.map(row=>row.Name)).size!==builders.length)fail('cleanup_registration_inventory_invalid');
  const owned=builders.filter(row=>row.Name===builder);
  if(owned.length>1||owned.some(row=>row.Driver!=='docker-container'||!Array.isArray(row.Nodes)||row.Nodes.length!==1||row.Nodes[0].Name!==builder+'0'||row.Nodes[0].Endpoint!==nativeDockerEndpoint||!isDeepStrictEqual(row.Nodes[0].DriverOpts,nativeDriverOptions())))fail('cleanup_builder_ownership_denied');
  const containers=await run(['container','ls','--all','--filter','name=^/'+container+'$','--format','{{json .Names}}'],10000);
  if(!successful(containers))fail('cleanup_container_inventory_unverified');
  const names=jsonLines(containers.stdout);if(names.some(name=>name!==container)||names.length>1)fail('cleanup_container_inventory_invalid');
  let observed=null;
  if(names.length){
    const inspected=await run(['container','inspect',container],10000);if(!successful(inspected))fail('cleanup_inspect_unverified');
    const rows=JSON.parse(inspected.stdout);if(rows.length!==1||rows[0].Name!=='/'+container||rows[0].Config?.Image!==bases.buildkit.reference)fail('cleanup_container_ownership_denied');
    observed={id:rows[0].Id,running:rows[0].State?.Running===true,status:rows[0].State?.Status,oom_killed:rows[0].State?.OOMKilled===true,exit_code:rows[0].State?.ExitCode};
  }
  return {registration_present:owned.length===1,container:observed};
}
async function cleanupNativeBuilder(builder,creation) {
  const container='buildx_buildkit_'+builder+'0',deadline=Date.now()+120000;
  const receipt={schema:'stackkits.pocketid-builder-cleanup.v1',builder,container,platform,docker_context:nativeDockerContext,daemon_id:nativeDockerDaemon,expected_registration:{driver:'docker-container',node:builder+'0',endpoint:nativeDockerEndpoint,driver_options:nativeDriverOptions()},creation,status:'unverified',operations:[]};
  const persist=()=>save(path.join(out,'builder-cleanup.json'),receipt);
  const run=async(args,limit)=>{
    const remaining=deadline-Date.now()-2500;if(remaining<=0)fail('cleanup_budget_exhausted');
    const r=await boundedDocker(args,Math.min(limit,remaining));
    receipt.operations.push({operation:args.slice(0,2).join(' '),exit:r.status,code:r.error?.code??null,signal:r.signal,timed_out:r.timed_out,process_group_absent:r.process_group_absent,stderr:r.stderr.slice(-4096)});persist();return r;
  };
  persist();
  try{
    // An unacknowledged create grants no removal authority. Preserve bounded,
    // exact-name readback and fail the original operation without any mutation.
    if(creation.stage!=='acknowledged'){
      const registrations=await run(['buildx','ls','--format','json','--no-trunc','--timeout','5s'],10000);
      receipt.unacknowledged_registrations=successful(registrations)?jsonLines(registrations.stdout).filter(row=>row.Name===builder).map(row=>({Name:row.Name,Driver:row.Driver,Nodes:row.Nodes?.map(node=>({Name:node.Name,Endpoint:node.Endpoint,DriverOpts:node.DriverOpts}))})):null;
      const containers=await run(['container','ls','--all','--filter','name=^/'+container+'$','--format','{{json .Names}}'],10000);
      receipt.unacknowledged_containers=successful(containers)?jsonLines(containers.stdout):null;
      persist();fail('creation_unacknowledged_read_only_no_cleanup');
    }
    receipt.before=await observeNativeBuilder(builder,run);persist();
    if(creation.result?.process_group_absent!==true)fail('creation_process_group_retained_unverified');
    if(receipt.before.container&&!receipt.before.registration_present)fail('cleanup_orphan_requires_reconciliation');
    if(receipt.before.container){
      const logs=await run(['container','logs','--tail','200',container],10000);
      if(successful(logs))fs.writeFileSync(path.join(out,'builder-daemon.log'),logs.stdout+logs.stderr);
      if(receipt.before.container.running){const stopped=await run(['container','stop','--time','10',container],20000);if(!successful(stopped))fail('cleanup_stop_unacknowledged');}
    }
    if(receipt.before.registration_present){const removed=await run(['buildx','rm','--force','--timeout','20s',builder],60000);if(!successful(removed))fail('cleanup_remove_unacknowledged');}
    receipt.after=await observeNativeBuilder(builder,run);
    if(receipt.after.registration_present||receipt.after.container!==null)fail('cleanup_resource_retained');
    receipt.status='verified-absent';persist();
  }catch(error){receipt.error=error.message;persist();throw error;}
}

if(mode==='build'){
  const publisherVersion=command('skopeo',['--version']).trim();
  const builderVersion=command('docker',['buildx','version']).trim();
  if(!command('docker',['buildx','rm','--help']).includes('--timeout'))fail('cleanup_interface_unsupported');
  if(fs.existsSync(out))fail('owned_output_exists');fs.mkdirSync(out,{recursive:true});
  if(hash(fs.readFileSync(path.join(here,'Dockerfile')))!==recipe.dockerfile_sha256)fail('recipe_changed');
  command('python3',[path.join(here,'prepare.py'),'--output',path.join(out,'source')],{timeout:90000});
  const checkout=path.join(out,'source','pocket-id-'+recipe.upstream_commit);
  fs.copyFileSync(path.join(here,'Dockerfile'),path.join(checkout,'Dockerfile'));
  const builder='pocketid-native-'+process.env.GITHUB_RUN_ID+'-'+process.env.GITHUB_RUN_ATTEMPT+'-'+arch;
  if(!/^pocketid-native-\d+-\d+-(amd64|arm64)$/.test(builder))fail('run_coordinates');
  let createAttempted=false,buildFailure;
  const contexts=JSON.parse(command('docker',['context','inspect']));
  if(contexts.length!==1||typeof contexts[0].Name!=='string'||contexts[0].Endpoints?.docker?.Host!=='unix:///var/run/docker.sock')fail('native_docker_context_unqualified');
  nativeDockerContext=contexts[0].Name;
  const daemon=await boundedDocker(['info','--format','{{json .ID}}'],10000);
  if(!successful(daemon)||typeof JSON.parse(daemon.stdout)!=='string'||!JSON.parse(daemon.stdout))fail('native_docker_daemon_unqualified');
  nativeDockerDaemon=JSON.parse(daemon.stdout);
  const beforeCreate=await observeNativeBuilder(builder,boundedDocker);
  if(beforeCreate.registration_present||beforeCreate.container!==null)fail('builder_preexisting_refused');
  const creation={schema:'stackkits.pocketid-builder-creation.v1',source_sha:source,tag,platform,builder,container:'buildx_buildkit_'+builder+'0',docker_context:nativeDockerContext,daemon_id:nativeDockerDaemon,endpoint:nativeDockerEndpoint,driver_options:nativeDriverOptions(),preflight:beforeCreate,stage:'not-attempted'};
  const saveCreation=()=>save(path.join(out,'builder-creation.json'),creation);saveCreation();
  save(path.join(out,'source-receipt.json'),{source_sha:source,tag,platform,upstream:recipe.upstream_commit,patch_sha256:recipe.patch_sha256,dockerfile_sha256:recipe.dockerfile_sha256,reference,build_completed:false,dockerfile_frontend:recipe.dockerfile_frontend});
  try{
    creation.stage='intent-before-effect';saveCreation();createAttempted=true;
    const created=await boundedDocker(['buildx','create','--name',builder,'--driver','docker-container','--driver-opt','image='+bases.buildkit.reference,'--driver-opt','memory=3g','--driver-opt','cpu-period=100000','--driver-opt','cpu-quota=200000',nativeDockerEndpoint],30000);
    creation.result={exit:created.status,code:created.error?.code??null,timed_out:created.timed_out,process_group_absent:created.process_group_absent,stderr:created.stderr.slice(-4096)};
    creation.stage=successful(created)?'acknowledged':'unacknowledged-inspect-without-replay';saveCreation();
    if(!successful(created))fail('builder_creation_unacknowledged');
    creation.acknowledged_registration=await observeNativeBuilder(builder,boundedDocker);
    if(!creation.acknowledged_registration.registration_present)fail('builder_registration_missing_after_ack');
    saveCreation();
    command('docker',['buildx','build','--builder',builder,'--platform',platform,'--provenance=mode=max','--sbom=true',
      '--build-arg','NODE_IMAGE='+bases.node.reference,'--build-arg','GO_IMAGE='+bases.go.reference,
      '--build-arg','RUNTIME_IMAGE='+bases.runtime.reference,'--build-arg','SOURCE_SHA='+source,'--build-arg','BUILD_TAGS=',
      '--metadata-file',path.join(out,'build-metadata.json'),'--output','type=oci,dest='+path.join(out,'image.oci.tar'),checkout],
      {timeout:20*60*1000,maxBuffer:16*1024*1024,stdio:'inherit'});
  }catch(error){buildFailure=error;throw error;}finally{if(createAttempted){try{await cleanupNativeBuilder(builder,creation);}catch(cleanupFailure){if(buildFailure)throw new AggregateError([buildFailure,cleanupFailure],'pocketid_native_build_and_cleanup_failed');throw cleanupFailure;}}}
  save(path.join(out,'build-running.json'),{tag:'pocketid-native:'+source+'-'+arch,platform});
  command('python3',[path.join(here,'load-oci.py')],{cwd:out,env:{...process.env,POCKETID_NATIVE_EVIDENCE_ROOT:out},timeout:120000});
  save(path.join(out,'source-preflight.json'),{license:recipe.license_sha256,bases:{'postgres:18':{pinned:bases.postgres.reference,manifest:bases.postgres.reference.split('@')[1]}}});
  save(path.join(out,'source-receipt.json'),{source_sha:source,tag,platform,upstream:recipe.upstream_commit,patch_sha256:recipe.patch_sha256,dockerfile_sha256:recipe.dockerfile_sha256,reference,skopeo_version:publisherVersion,buildx_version:builderVersion,node_version:process.versions.node,build_completed:true,dockerfile_frontend:recipe.dockerfile_frontend});
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
