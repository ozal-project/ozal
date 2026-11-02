// Browser regression for the update dialog (REL-019/REL-020).
// Runs a disposable real edge with a host-issued fixture session. Only update
// HTTP responses are mocked: no artifact is downloaded, no service is stopped,
// and these UI checks do not establish release provenance or MFA acceptance.
// API authorization and execution are covered by the Go seam tests.
//
// OZAL_BIN=/path/to/ozal node scripts/demo/update-acceptance.mjs
// OZAL_UPDATE_ARTIFACTS optionally retains screenshots without credentials.
import assert from 'node:assert/strict';
import { createServer } from 'node:net';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, mkdir, writeFile, rm } from 'node:fs/promises';
import { randomBytes } from 'node:crypto';
import { join, resolve } from 'node:path';
import { tmpdir } from 'node:os';

const exec = promisify(execFile);
const binary = process.env.OZAL_BIN && resolve(process.env.OZAL_BIN);
if (!binary) throw new Error('Set OZAL_BIN to the built binary');
const root = await mkdtemp(join(tmpdir(), 'ozal-update-ui-'));
const captures = process.env.OZAL_UPDATE_ARTIFACTS;
const session = `ozal-lock-ui-${process.pid}`;
const ab = async (...args) => {
  try {
    return JSON.parse((await exec('agent-browser', ['--session', session, '--args', '--no-sandbox', '--json', ...args], { maxBuffer: 4 << 20, timeout: 30_000 })).stdout);
  } catch {
    throw new Error(`Browser command ${args[0]} failed during ${step}`);
  }
};
const port = () => new Promise(resolve => {
  const server = createServer().listen(0, '127.0.0.1', () => {
    const n = server.address().port;
    server.close(() => resolve(n));
  });
});
let child, step = 'start';
try {
  if (captures) await mkdir(captures, { recursive: true });
  const admin = await port(), app = await port();
  const url = `http://127.0.0.1:${admin}`;
  const env = { ...process.env, OZAL_LOCK_UI_TOKEN: randomBytes(24).toString('hex') };
  const config = join(root, 'config.json'), db = join(root, 'state.db');
  await writeFile(config, JSON.stringify({ listen: `127.0.0.1:${app}`, admin: { listen: `127.0.0.1:${admin}`, socket: join(root, 'admin.sock'), token: 'env:OZAL_LOCK_UI_TOKEN' }, routes: [{ host: 'fixture.localhost', target: 'http://127.0.0.1:9' }] }));
  await exec(binary, ['migrate', '-db', db], { env });
  child = spawn(binary, ['-config', config, '-db', db], { env, stdio: 'ignore' });
  for (let i=0;;i++) {
    try { if ((await fetch(url+'/_/healthz')).ok) break; } catch {}
    if (i>100 || child.exitCode!==null) throw new Error('Disposable edge failed to start');
    await new Promise(r=>setTimeout(r,100));
  }
  const {stdout} = await exec(binary, ['console','login','-config',config,'-name','Update UI fixture','-tools','updates,updates.install','-url',url], {env});
  const link = stdout.split('\n').find(line=>line.startsWith('http'));
  assert(link, 'No fixture login URL');
  const id='a'.repeat(32), jobID='b'.repeat(32), now=new Date().toISOString();
  const plan={id,checked_at:now,installed:'v0.1.0',latest:'v0.2.0',channel:'stable',platform:'linux/arm64',result:'available',archive_sha256:'c'.repeat(64),size:20971520,migrations:true,notes_url:'https://ozal.dev/releases/v0.2.0'};
  const job={id:jobID,channel:'stable',from:'v0.1.0',to:'v0.2.0',phase:'staging',result:'pending',started_at:now,updated_at:now};
  const done={...job,phase:'complete',result:'updated',finished_at:now};
  await ab('open','about:blank');
  const mock=async(path,body)=>{
    await ab('network','unroute','**'+path);
    await ab('network','route','**'+path,'--body',JSON.stringify(body));
  };
  await mock('/_/api/updates/status',{checked_at:now,installed:'v0.1.0',latest:'v0.2.0',result:'available'});
  await mock('/_/api/updates/plan',plan);
  await mock('/_/api/updates/jobs/latest',done);
  await mock('/_/api/updates/jobs',job);
  await mock('/_/api/updates/jobs/'+jobID,job);
  await ab('open',link);
  await ab('wait','--text','Update UI fixture');
  step='desktop review';
  await ab('find','role','button','click','--name','Review update','--exact');
  await ab('wait','--text','linux/arm64');
  const desktop=await ab('eval','document.querySelector("dialog[open], [role=dialog]").textContent');
  assert.match(desktop.data.result,/20.0 MiB/);
  assert.match(desktop.data.result,/state database/);
  if (captures) await ab('screenshot',join(captures,'update-desktop.png'));
  await ab('press','Escape');
  const focus=await ab('eval','document.activeElement.textContent.trim()');
  assert.equal(focus.data.result,'Review update');
  step='mobile review';
  await ab('set','viewport','390','844');
  await ab('find','role','button','click','--name','Review update','--exact');
  await ab('wait','--text','linux/arm64');
  const bounds=await ab('eval','(() => { const r=document.querySelector("dialog[open], [role=dialog]").getBoundingClientRect(); return {left:r.left,right:r.right,width:innerWidth}; })()');
  assert(bounds.data.result.left>=0 && bounds.data.result.right<=bounds.data.result.width);
  const a11y=await ab('a11y','--selector','dialog[open], [role="dialog"]');
  const violations=a11y.data?.violations ?? a11y.data?.results?.violations;
  assert(Array.isArray(violations)); assert.equal(violations.length,0);
  if (captures) await ab('screenshot',join(captures,'update-mobile.png'));
  step='start reviewed job';
  await ab('find','role','button','click','--name','Update to v0.2.0','--exact');
  await ab('wait','--text','Downloading the verified archive');
  await ab('press','Escape');
  step='resume pending job';
  await mock('/_/api/updates/jobs/latest',job);
  await ab('find','role','button','click','--name','Review update','--exact');
  await ab('wait','--text','Downloading the verified archive');
  await mock('/_/api/updates/jobs/'+jobID,done);
  step='observe resumed job finishing';
  await ab('wait','--text','is installed and answering.','--timeout','10000');
  console.log('PASS: desktop review, Escape/focus, mobile bounds, dialog axe, pending-job resume');
} catch(e) {
  console.error(`FAIL during ${step}: ${e.message}`);
  try {
    const view = await ab('eval', 'document.querySelector("dialog[open], [role=dialog]")?.textContent?.trim()');
    console.error('Dialog fixture state:', String(view.data?.result ?? 'closed').slice(0, 1200));
  } catch {}
  process.exitCode=1;
} finally {
  try { await ab('close'); } catch {}
  if (child && child.exitCode === null) {
    const ended=new Promise(resolve=>child.once('exit',resolve));
    child.kill('SIGTERM');
    await ended;
  }
  await rm(root,{recursive:true,force:true});
}
