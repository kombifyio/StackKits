// Read authenticated native publication and emit a reviewable module patch.
// This tool never edits the source module, touches a Node or publishes a Registry tag.
import fs from 'node:fs';import path from 'node:path';import {execFileSync} from 'node:child_process';import {createHash} from 'node:crypto';
const args=process.argv.slice(2);const take=name=>args[args.indexOf(name)+1];
const tag=take('--tag'),source=take('--public-source'),runId=take('--run-id'),attempt=take('--attempt'),output=take('--output');
if(!args.includes('--tag')||!/^v\d+\.\d+\.\d+(?:(?:-beta\.|-edge\.)\d+)?$/.test(tag??'')||
  !args.includes('--public-source')||!/^[a-f0-9]{40}$/.test(source??'')||!args.includes('--run-id')||!/^[1-9]\d*$/.test(runId??'')||
  !args.includes('--attempt')||!/^[1-9]\d*$/.test(attempt??'')||!args.includes('--output')||!path.isAbsolute(output??'')||fs.existsSync(output))throw Error('pocketid_adoption_coordinates_required');
const execute=(bin,argv)=>execFileSync(bin,argv,{encoding:'utf8',stdio:['ignore','pipe','pipe'],timeout:60000,maxBuffer:8*1024*1024});
const api=()=>JSON.parse(execute('gh',['api',`repos/kombifyio/StackKits/actions/runs/${runId}`]));
function admit(run){if(run.id!==Number(runId)||run.run_attempt!==Number(attempt)||run.repository?.full_name?.toLowerCase()!=='kombifyio/stackkits'||run.head_repository?.full_name?.toLowerCase()!=='kombifyio/stackkits'||run.head_sha!==source||run.path!=='.github/workflows/publish-image.yml'||!['release','workflow_dispatch'].includes(run.event)||run.status!=='completed'||run.conclusion!=='success')throw Error('pocketid_native_run_not_qualified');}
admit(api());
const registry='ghcr.io/kombifyio/stackkits',reference=registry+':pocketid-cas-'+tag+'-g'+source;
const raw=execute('skopeo',['inspect','--raw','docker://'+reference]),digest='sha256:'+createHash('sha256').update(raw).digest('hex'),index=JSON.parse(raw);
const actual=index.manifests?.filter(m=>m.platform?.os==='linux'&&['amd64','arm64'].includes(m.platform.architecture));
if(actual?.length!==2||new Set(actual.map(m=>m.platform.architecture)).size!==2||index.manifests.some(m=>!(actual.includes(m)||m.platform?.architecture==='unknown'&&m.annotations?.['vnd.docker.reference.type']==='attestation-manifest')))throw Error('pocketid_platform_coverage_required');
const attestation=JSON.parse(execute('gh',['attestation','verify','oci://'+registry+'@'+digest,'--repo','kombifyio/StackKits',
  '--signer-workflow','github.com/kombifyio/StackKits/.github/workflows/publish-image.yml',
  '--signer-digest',source,'--source-digest',source,'--source-ref','refs/tags/'+tag,'--deny-self-hosted-runners','--format','json']));
const invocation=`https://github.com/kombifyio/StackKits/actions/runs/${runId}/attempts/${attempt}`;
if(!Array.isArray(attestation)||!attestation.some(entry=>entry.verificationResult?.statement?.predicate?.runDetails?.metadata?.invocationId?.toLowerCase()===invocation.toLowerCase()))throw Error('pocketid_attestation_original_attempt_required');
admit(api());
const moduleFile=path.resolve(import.meta.dirname,'../module.cue'),before=fs.readFileSync(moduleFile,'utf8');
const oldImage='image: "ghcr.io/pocket-id/pocket-id"',oldTag='tag:      "v2.16.0@sha256:9366436f3fd21619ed7e5709fa0acac88130f73414ec8ee1caf768fc487111ea"';
if(before.split(oldImage).length!==2||before.split(oldTag).length!==2)throw Error('pocketid_official_module_pin_drift');
const image='image: "'+registry+'"',pin='tag:      "pocketid-cas-'+tag+'-g'+source+'@'+digest+'"';
const imageLine=before.split('\n').findIndex(line=>line.trim()===oldImage)+1;
const tagLine=before.split('\n').findIndex(line=>line.trim()===oldTag)+1;
if(imageLine<1||tagLine<1)throw Error('pocketid_module_pin_line_drift');
fs.mkdirSync(output,{recursive:true});
fs.writeFileSync(path.join(output,'module.cue'),before.replace(oldImage,image).replace(oldTag,pin));
fs.writeFileSync(path.join(output,'module-adoption.patch'),`diff --git a/modules/pocketid/module.cue b/modules/pocketid/module.cue\n--- a/modules/pocketid/module.cue\n+++ b/modules/pocketid/module.cue\n@@ -${imageLine},1 +${imageLine},1 @@\n-\t\t${oldImage}\n+\t\t${image}\n@@ -${tagLine},1 +${tagLine},1 @@\n-\t\t${oldTag}\n+\t\t${pin}\n`);
fs.writeFileSync(path.join(output,'publication-readback.json'),JSON.stringify({schema:'stackkits.pocketid-module-adoption.v1',source_sha:source,tag,run_id:Number(runId),attempt:Number(attempt),reference,index_digest:digest,platforms:actual.map(m=>({platform:'linux/'+m.platform.architecture,digest:m.digest})),native_attestation_verified:true,all_platforms_native_qualified:true,primary_module_source_written:false,candidate_module_written:true,node_mutated:false,node_rollout_admitted:false,required_followup:'Regenerate affected module facts/catalog through existing product generation; publish reviewed source; execute the integrated existing-node Docker/SQLite drain lifecycle and independently read its signed binding before CAS claims.'},null,2)+'\n');
console.log(JSON.stringify({index_digest:digest,platforms:['linux/amd64','linux/arm64'],output,node_mutated:false}));
