import fs from 'node:fs';import path from 'node:path';import {execFileSync,spawnSync} from 'node:child_process';import {createHash} from 'node:crypto';
const recipe=JSON.parse(fs.readFileSync(new URL('./build-recipe.json',import.meta.url)));
const run=(bin,args)=>execFileSync(bin,args,{encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:60000,maxBuffer:8*1024*1024});
const source=run('git',['rev-parse','HEAD']).trim(),tag=process.env.TAG;
if(process.env.GITHUB_REPOSITORY?.toLowerCase()!=='kombifyio/stackkits'||source!==process.env.GITHUB_SHA||!/^v\d+\.\d+\.\d+(?:(?:-beta\.|-edge\.)\d+)?$/.test(tag??''))throw Error('pocketid_merge_context');
const root=path.join(process.env.RUNNER_TEMP,'pocketid-native-receipts'),platforms=[];
const hash=bytes=>'sha256:'+createHash('sha256').update(bytes).digest('hex');
for(const arch of ['amd64','arm64']){
  const file=path.join(root,`pocketid-native-${arch}-${process.env.GITHUB_RUN_ID}-${process.env.GITHUB_RUN_ATTEMPT}`,'publication.json');
  const proof=JSON.parse(fs.readFileSync(file));
  if(proof.source_sha!==source||proof.tag!==tag||proof.platform!=='linux/'+arch||proof.passed_cases!==9||
    proof.native_run!==Number(process.env.GITHUB_RUN_ID)||proof.native_attempt!==Number(process.env.GITHUB_RUN_ATTEMPT)||
    proof.patch_sha256!==recipe.patch_sha256||proof.license_sha256!==recipe.license_sha256||
    proof.reference!==recipe.registry+':pocketid-cas-'+tag+'-'+arch+'-g'+source||
    !/^sha256:[a-f0-9]{64}$/.test(proof.index_digest)||!/^sha256:[a-f0-9]{64}$/.test(proof.platform_manifest))throw Error('pocketid_merge_qualification');
  const raw=run('skopeo',['inspect','--raw','docker://'+recipe.registry+'@'+proof.index_digest]);
  if(hash(raw)!==proof.index_digest)throw Error('pocketid_merge_registry_digest');
  const index=JSON.parse(raw);if(!Array.isArray(index.manifests)||!index.manifests.some(m=>m.digest===proof.platform_manifest&&m.platform?.os==='linux'&&m.platform.architecture===arch))throw Error('pocketid_merge_platform');
  platforms.push({...proof,descriptors:index.manifests});
}
const reference=recipe.registry+':pocketid-cas-'+tag+'-g'+source;
const expected=[...new Set(platforms.flatMap(p=>p.descriptors.map(m=>m.digest)))].sort();
function inspectIndex(raw){const index=JSON.parse(raw);if(!Array.isArray(index.manifests)||JSON.stringify(index.manifests.map(m=>m.digest).sort())!==JSON.stringify(expected))throw Error('pocketid_index_conflict');return index;}
const current=spawnSync('skopeo',['inspect','--raw','docker://'+reference],{encoding:'utf8',timeout:30000});
if(current.status===0)inspectIndex(current.stdout);
else{
  if(!/manifest unknown|manifest_unknown|name unknown/i.test(current.stderr??''))throw Error('pocketid_index_read_failed');
  run('docker',['buildx','imagetools','create','--tag',reference,...platforms.map(p=>recipe.registry+'@'+p.index_digest)]);
}
const raw=run('skopeo',['inspect','--raw','docker://'+reference]);inspectIndex(raw);const digest=hash(raw);
const receipt={schema:'stackkits.pocketid-publication.v1',repository:'kombifyio/StackKits',source_sha:source,release_tag:tag,reference,index_digest:digest,
  upstream:recipe.upstream_commit,patch_sha256:recipe.patch_sha256,license_sha256:recipe.license_sha256,
  native_run:Number(process.env.GITHUB_RUN_ID),native_attempt:Number(process.env.GITHUB_RUN_ATTEMPT),
  platforms:platforms.map(({descriptors,...proof})=>proof),product_node_acceptance:false};
fs.writeFileSync(path.join(process.env.RUNNER_TEMP,'pocketid-publication.json'),JSON.stringify(receipt,null,2)+'\n');
fs.appendFileSync(process.env.GITHUB_OUTPUT,'digest='+digest+'\n');console.log(JSON.stringify({reference,index_digest:digest,platforms:platforms.map(p=>p.platform)}));
