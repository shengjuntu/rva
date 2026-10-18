// Integration acceptance: real HTTP -> HyperFrames/browser -> FFmpeg -> MP4.
// Creates an explicit test project; no RunDesk/model invocation or news content.
import {readFileSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {strict as assert} from 'node:assert';
const data = process.env.VIDEO_DATA || '/data';
const base = process.env.VIDEO_SMOKE_URL || 'http://127.0.0.1:'+(process.env.VIDEO_LISTEN||'0.0.0.0:3220').split(':').at(-1);
const token = process.env.VIDEO_APP_TOKEN || readFileSync(data+'/access-token','utf8').trim();
async function api(path,body,method) {
  const form=body instanceof FormData;
  const response=await fetch(base+path,{method:method||(body?'POST':'GET'),headers:{Authorization:'Bearer '+token,...(!form?{'Content-Type':'application/json'}:{})},body:body?(form?body:JSON.stringify(body)):undefined,signal:AbortSignal.timeout(30000)});
  const value=await response.json(); if(!response.ok)throw Error(response.status+' '+JSON.stringify(value));return value;
}
let p=await api('/api/projects',{name:'Docker 验收 · 测试音与标题卡'});
const route='/api/projects/'+p.id;
const dir=mkdtempSync(join(tmpdir(),'video-acceptance-'));
try {
  const wave=join(dir,'test-tone.wav');
  const tone=spawnSync('ffmpeg',['-v','error','-f','lavfi','-i','sine=frequency=440:sample_rate=48000:duration=2','-y',wave],{encoding:'utf8',timeout:30000});
  assert.equal(tone.status,0,tone.stderr);
  const form=new FormData();form.append('file',new Blob([readFileSync(wave)],{type:'audio/wav'}),'测试音.wav');
  await api(route+'/assets',form);
  p=await api(route);
  const asset=p.assets.find(a=>a.mime.startsWith('audio/'));assert.ok(asset);
  p=await api(route,{...p,shots:[{id:'',title:'视频环境验收',text:'中文标题 · English · 测试音',duration:2,assetId:''}],audio:{narration:{assetId:asset.id,volume:0.1,start:0,sourceStart:0,loop:false}}},'PUT');
  const job=await api(route+'/render',{});
  const deadline=Date.now()+600000;
  let rendered;
  while(Date.now()<deadline) {
    p=await api(route);rendered=p.renders.find(j=>j.id===job.id);
    if(rendered?.state==='completed')break;
    if(['failed','canceled','interrupted'].includes(rendered?.state))throw Error(JSON.stringify(rendered));
    await new Promise(r=>setTimeout(r,1500));
  }
  assert.equal(rendered?.state,'completed','render timed out');
  const output=join(data,'projects',p.id,'renders',job.id,'final.mp4');
  const result=spawnSync('ffprobe',['-v','error','-show_format','-show_streams','-of','json',output],{encoding:'utf8',timeout:30000});
  assert.equal(result.status,0,result.stderr);
  const probe=JSON.parse(result.stdout),video=probe.streams.find(s=>s.codec_type==='video'),audio=probe.streams.find(s=>s.codec_type==='audio');
  assert.equal(video?.width,1080);assert.equal(video?.height,1920);assert.equal(video?.codec_name,'h264');assert.equal(audio?.codec_name,'aac');
  assert.ok(Math.abs(Number(probe.format.duration)-2)<0.2);
  console.log(JSON.stringify({status:'passed',projectId:p.id,renderId:job.id,output,width:video.width,height:video.height,duration:probe.format.duration,hasAudio:true,modelTested:false,visualReviewRequired:true},null,2));
} finally {rmSync(dir,{recursive:true,force:true});}
