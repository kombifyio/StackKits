import assert from 'node:assert/strict';
import {createRequire} from 'node:module';
import {readFileSync,writeFileSync} from 'node:fs';
import {createHash,randomBytes,randomUUID} from 'node:crypto';
import {spawnSync} from 'node:child_process';
import net from 'node:net';
const out=process.env.POCKETID_NATIVE_EVIDENCE_ROOT;
if(!out)throw Error('native evidence root required');
const preflight=JSON.parse(readFileSync(`${out}/source-preflight.json`));
const image=JSON.parse(readFileSync(`${out}/image-content.json`));
const require=createRequire(import.meta.url);
const {Client}=require('pg');
const nonce=randomBytes(5).toString('hex'), prefix=`m03-pid-${nonce}`;
const names={network:`${prefix}-net`,pg:`${prefix}-pg`,app:`${prefix}-app`};
const dbPassword=randomBytes(24).toString('hex'),apiKey=randomBytes(32).toString('hex'),encryptionKey=randomBytes(32).toString('hex');
const secretValues=[dbPassword,apiKey,encryptionKey];
const clean=text=>secretValues.reduce((s,v)=>s.replaceAll(v,'[redacted]'),String(text));
const started=new Date().toISOString();
const requests=[],cases=[],resources=[];
let admin,lockClient,base;
const cmd=(args,opts={})=>{
 const r=spawnSync('docker',args,{windowsHide:true,encoding:'utf8',timeout:30000,...opts});
 if(r.error||r.status!==0)throw Error(clean(`docker ${args.slice(0,3).join(' ')} failed: ${r.error?.message??r.stderr}`));return r.stdout.trim();
};
const foreignBefore=cmd(['ps','-q']);
const pause=ms=>new Promise(resolve=>setTimeout(resolve,ms));
async function freePort(){const server=net.createServer();await new Promise((r,j)=>server.once('error',j).listen(0,'127.0.0.1',r));const p=server.address().port;await new Promise(r=>server.close(r));return p;}
async function request(method,path,body,headers={},authorized=true){
 const response=await fetch(base+path,{method,redirect:'error',signal:AbortSignal.timeout(12000),headers:{...(authorized?{'X-API-Key':apiKey}:{}),...(body===undefined?{}:{'Content-Type':'application/json'}),...headers},body:body===undefined?undefined:JSON.stringify(body)});
 const text=await response.text();let json;try{json=JSON.parse(text);}catch{}
 const result={status:response.status,etag:response.headers.get('etag'),cache:response.headers.get('cache-control'),required:response.headers.get('x-pocketid-membership-required'),contract:response.headers.get('x-pocketid-membership-contract'),body:json};
 requests.push({method,path,status:result.status,etag:result.etag,cache:result.cache,required:result.required});
 return result;
}
function status(result,expected){assert.equal(result.status,expected,`HTTP ${result.status}: ${JSON.stringify(result.body)?.slice(0,300)}`);return result;}
function strong(tag,target){assert.match(tag,new RegExp(`^"pid-membership-v1:${target}:\\d+"$`));}
async function snapshot(id,kind='users'){const r=status(await request('GET',`/api/${kind}/${id}/membership`),200);strong(r.etag,`${kind==='users'?'user':'group'}:${id}`);assert.equal(r.cache,'no-store');assert.equal(r.contract,'pocketid-membership-cas-v1');return r;}
async function put(id,groups,tag,required=true){return request('PUT',`/api/users/${id}/user-groups`,{userGroupIds:groups},{...(tag?{'If-Match':tag}:{}),...(required?{'X-PocketID-Membership-Mode':'required'}:{})});}
async function check(name,run){const t=Date.now();try{await run();cases.push({name,passed:true,ms:Date.now()-t});console.log(`PASS ${name}`);}catch(e){cases.push({name,passed:false,error:clean(e.message),ms:Date.now()-t});throw e;}}
async function ready(){for(let i=0;i<50;i++){try{const r=await request('GET','/api/membership-capabilities');if(r.status===200)return r;}catch{}await pause(200);}throw Error('PocketID readiness failed');}
async function blocked(n){for(let i=0;i<30;i++){const {rows}=await admin.query("SELECT count(*)::int n FROM pg_stat_activity WHERE datname='pocketid' AND usename='pocketid' AND wait_event_type='Lock' AND query LIKE 'UPDATE membership_clock%'");if(rows[0].n>=n)return rows[0].n;await pause(50);}throw Error(`expected ${n} actual PostgreSQL fence waiters`);}
try {
 cmd(['network','create',names.network]);resources.push('network');
 cmd(['run','--name',names.pg,'--network',names.network,'--network-alias','db','--cpus','1','--memory','512m','--env','POSTGRES_PASSWORD','--publish','127.0.0.1::5432','--detach',preflight.bases['postgres:18'].pinned],{env:{...process.env,POSTGRES_PASSWORD:dbPassword},timeout:120000});resources.push('pg');
 const pgPort=cmd(['port',names.pg,'5432/tcp']).split(':').at(-1);
 const adminUrl=`postgresql://postgres:${dbPassword}@127.0.0.1:${pgPort}/postgres`;
 for(let i=0;i<30;i++){const r=spawnSync('docker',['exec',names.pg,'pg_isready','-h','127.0.0.1','-U','postgres'],{windowsHide:true,stdio:'ignore',timeout:3000});if(r.status===0)break;await pause(250);}
 admin=new Client({connectionString:adminUrl,statement_timeout:8000});await admin.connect();
 await admin.query(`CREATE ROLE pocketid LOGIN PASSWORD '${dbPassword}' NOSUPERUSER NOBYPASSRLS NOCREATEDB NOCREATEROLE`);
 await admin.query('CREATE DATABASE pocketid OWNER pocketid');
 await admin.end();admin=new Client({connectionString:`postgresql://postgres:${dbPassword}@127.0.0.1:${pgPort}/pocketid`,statement_timeout:8000});await admin.connect();
 const port=await freePort();base=`http://127.0.0.1:${port}`;
 const appEnv={...process.env,STATIC_API_KEY:apiKey,ENCRYPTION_KEY:encryptionKey,DB_CONNECTION_STRING:`postgres://pocketid:${dbPassword}@db:5432/pocketid?sslmode=disable`,APP_URL:`http://localhost:${port}`};
 cmd(['run','--name',names.app,'--network',names.network,'--cpus','2','--memory','384m','--publish',`127.0.0.1:${port}:1411`,'--env','STATIC_API_KEY','--env','ENCRYPTION_KEY','--env','DB_CONNECTION_STRING','--env','APP_URL','--env','ANALYTICS_DISABLED=true','--env','VERSION_CHECK_DISABLED=true','--env','GOMAXPROCS=2','--env','GOMEMLIMIT=256MiB','--detach',image.imageId],{env:appEnv});resources.push('app');
 const capability=await ready();assert.equal(capability.body.contract,'pocketid-membership-cas-v1');
 const migrations=await admin.query('SELECT * FROM schema_migrations');
 assert.equal(String(migrations.rows[0].version),'20261025060000');assert.equal(migrations.rows[0].dirty,false);
 const posture=await admin.query("SELECT rolname,rolsuper,rolbypassrls FROM pg_roles WHERE rolname='pocketid'");
 const version=await admin.query('SHOW server_version');
 writeFileSync(`${out}/runtime-custody.json`,JSON.stringify({names,origin:base,imageId:image.imageId,postgres:preflight.bases['postgres:18'].manifest,migrations:migrations.rows,role:posture.rows,serverVersion:version.rows,localIsolated:true},null,2));
 await check('authenticated capability and embedded frontend available',async()=>{
   status(await request('GET','/api/membership-capabilities',undefined,{},false),401);
   status(await request('GET','/',undefined,{},false),200);
 });
 const user=status(await request('POST','/api/users',{id:randomUUID(),username:`cas_${nonce}`,email:`cas-${nonce}@example.invalid`,firstName:'Protocol'}),201).body;
 const legacy=status(await request('POST','/api/users',{id:randomUUID(),username:`legacy_${nonce}`,email:`legacy-${nonce}@example.invalid`,firstName:'Manual'}),201).body;
 const group=status(await request('POST','/api/user-groups',{name:`cas_${nonce}`,friendlyName:'Protocol group'}),201).body;
 let current;
 await check('unmanaged manual membership compatibility',async()=>{
   status(await put(legacy.id,[group.id],undefined,false),200);
   const s=await snapshot(legacy.id);assert.equal(s.required,'false');assert.deepEqual(s.body.userGroups.map(g=>g.id),[group.id]);
   status(await put(legacy.id,[],undefined,false),200);
 });
 await check('strong snapshot and no-op revoke fences delayed stale grant',async()=>{
   const old=await snapshot(user.id);assert.equal(old.required,'false');
   const noOp=status(await put(user.id,[],old.etag),200);strong(noOp.etag,`user:${user.id}`);assert.notEqual(noOp.etag,old.etag);
   status(await put(user.id,[group.id],old.etag),412);
   current=await snapshot(user.id);assert.equal(current.required,'true');assert.deepEqual(current.body.userGroups,[]);assert.equal(current.etag,noOp.etag);
   for(const invalid of ['*',`W/${current.etag}`])status(await put(user.id,[group.id],invalid),400);
   assert.equal((await snapshot(user.id)).etag,current.etag);
 });
 await check('legacy direct and inverse writers cannot mutate a managed UUID',async()=>{
   status(await put(user.id,[group.id],undefined,false),428);
   status(await request('PUT',`/api/user-groups/${group.id}/users`,{userIds:[user.id]}),428);
   assert.deepEqual((await snapshot(user.id)).body.userGroups,[]);
 });
 await check('two concurrent conditional writers accept exactly one',async()=>{
   const old=await snapshot(user.id);
   const result=await Promise.all([put(user.id,[group.id],old.etag),put(user.id,[],old.etag)]);
   assert.deepEqual(result.map(r=>r.status).sort(),[200,412]);
   const accepted=result[0].status===200?[group.id]:[];
   current=await snapshot(user.id);assert.deepEqual(current.body.userGroups.map(g=>g.id),accepted);
   const row=await admin.query('SELECT revision,conditional_required FROM membership_versions WHERE user_id=$1',[user.id]);assert.equal(row.rows[0].conditional_required,true);
 });
 await check('inverse form snapshot refuses stale edits then returns committed next ETag',async()=>{
   const oldGroup=await snapshot(group.id,'user-groups');
   current=await snapshot(user.id);status(await put(user.id,[],current.etag),200);
   status(await request('PUT',`/api/user-groups/${group.id}/users`,{userIds:[user.id]},{'If-Match':oldGroup.etag}),412);
   const fresh=await snapshot(group.id,'user-groups');
   const committed=status(await request('PUT',`/api/user-groups/${group.id}/users`,{userIds:[user.id]},{'If-Match':fresh.etag}),200);
   strong(committed.etag,`group:${group.id}`);assert.notEqual(committed.etag,fresh.etag);assert.equal((await snapshot(group.id,'user-groups')).etag,committed.etag);
 });
 await check('actual process restart retains managed flag and rejects pre-restart replay',async()=>{
   const old=await snapshot(user.id);status(await put(user.id,[],old.etag),200);
   const committed=await snapshot(user.id);
   const before=cmd(['inspect','--format','{{.State.StartedAt}}',names.app]);
   cmd(['restart',names.app]);await ready();
   const after=cmd(['inspect','--format','{{.State.StartedAt}}',names.app]);assert.notEqual(after,before);
   const restored=await snapshot(user.id);assert.equal(restored.etag,committed.etag);assert.equal(restored.required,'true');assert.deepEqual(restored.body.userGroups,[]);
   status(await put(user.id,[group.id],old.etag),412);status(await put(user.id,[group.id],undefined,false),428);
 });
 await check('DELETE and group profile HTTP requests serialize on real PostgreSQL fence',async()=>{
   const g=status(await request('POST','/api/user-groups',{name:`delete_${nonce}`,friendlyName:'Delete profile race'}),201).body;
   status(await request('PUT',`/api/user-groups/${g.id}`,{name:`delete_${nonce}`,friendlyName:'Normal profile edit'}),200);
   const condition=await snapshot(g.id,'user-groups');
   lockClient=new Client({connectionString:`postgresql://postgres:${dbPassword}@127.0.0.1:${pgPort}/pocketid`,statement_timeout:10000});await lockClient.connect();await lockClient.query('BEGIN');await lockClient.query('UPDATE membership_clock SET revision=revision WHERE id=1');
   const deleting=request('DELETE',`/api/user-groups/${g.id}`,undefined,{'If-Match':condition.etag});
   await blocked(1);
   const updating=request('PUT',`/api/user-groups/${g.id}`,{name:`delete_${nonce}`,friendlyName:'Late profile edit'});
   await blocked(2);await lockClient.query('COMMIT');await lockClient.end();lockClient=null;
   status(await deleting,204);status(await updating,404);status(await request('GET',`/api/user-groups/${g.id}`),404);
   const readback=await admin.query('SELECT count(*)::int n FROM user_groups WHERE id=$1',[g.id]);assert.equal(readback.rows[0].n,0);
 });
 await check('managed group delete and user delete cannot bypass conditions',async()=>{
   current=await snapshot(user.id);status(await put(user.id,[group.id],current.etag),200);
   status(await request('DELETE',`/api/user-groups/${group.id}`),428);
   const gs=await snapshot(group.id,'user-groups');status(await request('DELETE',`/api/user-groups/${group.id}`,undefined,{'If-Match':gs.etag}),204);
   current=await snapshot(user.id);assert.deepEqual(current.body.userGroups,[]);
   status(await request('DELETE',`/api/users/${user.id}`),428);
   status(await request('DELETE',`/api/users/${user.id}`,undefined,{'If-Match':current.etag}),204);
   status(await put(user.id,[],current.etag),404);
 });
 const binary=cmd(['exec',names.app,'sha256sum','/app/pocket-id']).split(' ')[0];
 const license=cmd(['exec',names.app,'sha256sum','/usr/share/licenses/pocket-id/LICENSE']).split(' ')[0];assert.equal(license,preflight.license);
 writeFileSync(`${out}/runtime-binary.json`,JSON.stringify({binarySHA256:binary,licenseSHA256:license,actualImage:cmd(['inspect','--format','{{.Image}}',names.app])},null,2));
}catch(error){console.error(clean(error.stack));process.exitCode=1;}
finally{
 if(lockClient){await lockClient.query('ROLLBACK').catch(()=>{});await lockClient.end();}
 if(admin)await admin.end();
 if(resources.includes('app')){
   const logs=spawnSync('docker',['logs',names.app],{windowsHide:true,encoding:'utf8',timeout:10000});writeFileSync(`${out}/provider.log`,clean((logs.stdout??'')+(logs.stderr??'')));
 }
 const cleanup={};
 for(const type of ['app','pg'])if(resources.includes(type)){const r=spawnSync('docker',['rm','-f',names[type]],{windowsHide:true,encoding:'utf8',timeout:15000});cleanup[type]=r.status===0;}
 if(resources.includes('network')){const r=spawnSync('docker',['network','rm',names.network],{windowsHide:true,encoding:'utf8',timeout:15000});cleanup.network=r.status===0;}
 const foreignAfter=cmd(['ps','-q']);
 writeFileSync(`${out}/protocol-receipt.json`,JSON.stringify({started,finished:new Date().toISOString(),scope:'isolated actual PocketID container/PostgreSQL HTTP authorization protocol; not Cloud/Node/Photos/Files acceptance',image:image.imageId,origin:base,names,cases,requests,cleanup,foreignContainersUnchanged:foreignBefore===foreignAfter,noExternalProvider:true,noPublishedImage:true},null,2));
 console.log(JSON.stringify({passed:cases.filter(c=>c.passed).length,failed:cases.filter(c=>!c.passed).length,cleanup,foreignContainersUnchanged:foreignBefore===foreignAfter}));
}
