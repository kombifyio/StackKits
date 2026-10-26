// Closed native HTTP client for a PROVISIONAL qualified Runtipi image. No exec
// endpoint, shell, Docker workload control, token file or background service.
const fail = () => process.exit(74);
try {
  const p = JSON.parse(process.argv[1]);
  if (!['actuate', 'lookup', 'open', 'close', 'derive', 'cancel', 'resolve', 'qualify'].includes(p.mode)) fail();
  const chunks = []; let size = 0;
  for await (const part of process.stdin) { size += part.length; if (size > 65536) fail(); chunks.push(part); }
  const secret = Buffer.concat(chunks); const credentials = JSON.parse(secret.toString('utf8')); secret.fill(0);
  if (typeof credentials.username !== 'string' || typeof credentials.password !== 'string') fail();
  const origin = 'http://127.0.0.1:3000'; let cookie = '';
  async function request(method, path, body) {
    const response = await fetch(origin + path, { method, redirect: 'error', signal: AbortSignal.timeout(290000),
      headers: { Origin: origin, 'Content-Type': 'application/json', ...(cookie ? { Cookie: cookie } : {}) },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }) });
    if (path === '/api/auth/login') cookie = response.headers.getSetCookie().map(v => v.split(';')[0]).join('; ');
    const reader = response.body.getReader(); const parts = []; let total = 0;
    for (;;) { const item = await reader.read(); if (item.done) break; total += item.value.length; if (total > (path === '/api/app-context' ? 8388608 : 262144)) { await reader.cancel(); fail(); } parts.push(Buffer.from(item.value)); }
    if (!response.ok) throw new Error('native HTTP refused');
    return JSON.parse(Buffer.concat(parts).toString('utf8'));
  }
  const auth = await request('POST', '/api/auth/login', credentials);
  credentials.username = ''; credentials.password = '';
  if (auth.success !== true || auth.totpSessionId || !cookie) fail();
  const appContext=await request('GET','/api/app-context'); if(appContext.user?.id!==p.principalId || appContext.user?.operator!==true) fail();
  const capability = await request('GET', '/api/app-controls/capability');
  if (capability.apiVersion !== 'runtipi.app-control/v1' || capability.baseVersion !== '4.10.2' || capability.managerId !== p.body.managerId || capability.sourceDigest !== p.body.sourceDigest) fail();
  if (p.mode === 'qualify') { process.exit(73); }
  if (p.mode === 'derive') { const recipe=await request('GET','/api/app-controls/apps/'+encodeURIComponent(p.urn)+'/recipe'); if(recipe.appUrn!==p.urn || recipe.managerId!==p.body.managerId || recipe.sourceDigest!==p.body.sourceDigest) fail(); process.stdout.write(JSON.stringify(recipe));process.exit(0); }
  const path = '/api/app-controls/apps/' + encodeURIComponent(p.urn) + '/groups/' + p.group;
  if (p.mode === 'open') {
    const { predecessorOperationId, action, effect, ...intent } = p.body;
    const recipe = await request('GET', '/api/app-controls/apps/' + encodeURIComponent(p.urn) + '/recipe');
    if (recipe.appId !== p.appId || recipe.recipeDigest !== intent.recipeDigest || recipe.effectiveInputDigest !== intent.effectiveInputDigest) fail();
    const group = await request('POST', path, intent);
    if (group.closedAt || group.groupId !== p.group || group.appUrn !== p.urn || group.appId !== p.appId || group.principalId !== p.principalId) fail();
    // Group rows do not store apiVersion; it was authenticated above via capability.
    for (const [key,value] of Object.entries(intent).filter(([key]) => key !== 'apiVersion')) if (JSON.stringify(group[key]) !== JSON.stringify(value)) fail();
    process.exit(0);
  }
  if (p.mode === 'close') { const closed = await request('POST', path + '/close', p.close); if (closed.groupId !== p.group || !closed.closedAt || closed.lastOperationId !== p.close.lastOperationId || closed.completionDigest !== p.close.completionDigest) fail(); process.exit(0); }
  const operationPath = path + '/operations/' + p.operation;
  if (p.mode === 'actuate') {
    // One PUT. Failure/timeout/lost response never leads to another PUT. Only
    // the exact original GET below can supply subsequent terminal evidence.
    try { await request('PUT', operationPath, p.body); } catch { }
  }
  if (p.mode === 'cancel' || p.mode === 'resolve') { await request('POST', operationPath + '/' + p.mode, p.close); }
  const result = await request('GET', operationPath);
  if (['lookup','cancel','resolve'].includes(p.mode)) { process.stdout.write(JSON.stringify(result)); process.exit(0); }
  for (const [key, value] of Object.entries(p.body)) if (JSON.stringify(result[key]) !== JSON.stringify(value)) fail();
  if (result.operationId !== p.operation || result.requestId !== p.operation || result.groupId !== p.group || result.appId !== p.appId || result.appUrn !== p.urn || result.principalId !== p.principalId || result.terminal !== true) fail();
  const outcome = result.state === 'unresolved' ? result.resolution?.outcome : result;
  const witness = result.state === 'unresolved' ? result.resolution?.witnessDigest : result.witnessDigest;
  if (outcome?.state !== 'succeeded' || outcome.composeExit !== 0 || outcome.effectiveInputDigest !== p.body.effectiveInputDigest || !/^[a-f0-9]{64}$/.test(witness)) fail();
  // Distinctive code only after the bound native operation has durably settled.
  // Outer exec failure is never made success from current container power.
  process.exit(73);
} catch { fail(); }
