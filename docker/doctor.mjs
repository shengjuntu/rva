import {readFileSync, existsSync, accessSync, constants} from 'node:fs';
import {spawnSync} from 'node:child_process';
const root = process.env.VIDEO_DATA || '/data';
const port = (process.env.VIDEO_LISTEN || '0.0.0.0:3220').split(':').at(-1);
const token = process.env.VIDEO_APP_TOKEN || readFileSync(root+'/access-token','utf8').trim();
const health = process.argv.includes('--health');
const env = await fetch(`http://127.0.0.1:${port}/api/${health ? "health" : "environment"}`, {headers:{Authorization:'Bearer '+token},signal:AbortSignal.timeout(health ? 7000 : 30000)}).then(async r=>{if(!r.ok)throw Error('App HTTP '+r.status);return r.json()});
if (health) process.exit(0); // Liveness is independent of RunDesk availability.
const checks = {...env.checks};
for (const [name,path,args] of [
  ['browser', process.env.HYPERFRAMES_BROWSER_PATH, ['--version']],
  ['espeak', '/usr/bin/espeak-ng', ['--version']],
]) {
  const run = path && existsSync(path) ? spawnSync(path,args,{encoding:'utf8',timeout:10000}) : null;
  checks[name] = {ready:run?.status===0,path,version:run?.stdout?.split('\n')[0]};
}
try { accessSync(root,constants.W_OK); checks.data = {ready:true}; } catch { checks.data={ready:false}; }
let runtime;
if(env.instanceId) {
  const r=await fetch(`http://127.0.0.1:${port}/api/runtime`,{headers:{Authorization:'Bearer '+token},signal:AbortSignal.timeout(10000)});
  runtime=await r.json();
}
console.log(JSON.stringify({appVersion:env.version,checks,rundeskReady:env.rundeskReady,rundeskError:env.rundeskError,runtime,renderTested:false,modelTested:false},null,2));
if(Object.values(checks).some(c=>!c.ready)||!env.rundeskReady) process.exitCode=1;
