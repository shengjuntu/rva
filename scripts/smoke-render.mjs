// Integration acceptance: real HTTP -> HyperFrames/browser -> FFmpeg -> MP4.
// Creates an explicit test project; no RunDesk/model invocation or news content.
import {readFileSync, mkdtempSync, rmSync} from 'node:fs';
import {tmpdir} from 'node:os';
import {join} from 'node:path';
import {spawnSync} from 'node:child_process';
import {createHash,randomBytes} from 'node:crypto';
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
  const view=await api(route+'/hyperframes');
  const source=`<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><link rel="stylesheet" href="runtime/font.css"><script src="runtime/gsap.min.js"></script><style>*{box-sizing:border-box}body{margin:0}#smoke{position:relative;width:320px;height:180px;overflow:hidden;background:#101820;color:white;font-family:VideoFont,sans-serif}h1{margin:24px;font-size:24px}.bar{position:absolute;left:24px;top:120px;width:60px;height:14px;background:#61dec2}</style></head><body><main id="smoke" data-composition-id="smoke" data-start="0" data-duration="2" data-width="320" data-height="180" data-fps="30"><section id="card" class="clip" data-start="0" data-duration="2" data-track-index="1"><h1>中文标题 · English<br>动画与测试音</h1><div class="bar"></div></section><audio id="tone" src="media/tone.wav" data-start="0" data-duration="2" data-end="2" data-track-index="4" data-volume="0.1"></audio></main><script>const timeline=gsap.timeline({paused:true});timeline.to('.bar',{x:180,duration:2,ease:'none'},0);window.__timelines=window.__timelines||{};window.__timelines.smoke=timeline;</script></body></html>`;
  const document={schemaVersion:1,entrypoint:'index.html',canvas:{width:320,height:180,fps:30,duration:2},hasAudio:true,files:[{path:'index.html',content:source}],assets:[{assetId:asset.id,path:'media/tone.wav'}],textSourceHash:view.sourceHash,timeline:[{id:'card',track:'visual',kind:'animation',start:0,duration:2},{id:'tone',track:'audio',kind:'audio',start:0,duration:2,assetId:asset.id}]};
  const saved=await api(route+'/hyperframes',{revision:p.revision,document},'PUT');
  const job=await api(route+'/render',{revision:saved.revision,requestId:randomBytes(12).toString('hex'),kind:'draft'});
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
  assert.equal(rendered.engine,'hyperframes_authored');
  assert.equal(video?.width,320);assert.equal(video?.height,180);assert.equal(video?.codec_name,'h264');assert.equal(audio?.codec_name,'aac');
  assert.ok(Math.abs(Number(probe.format.duration)-2)<0.2);
  const frameHashes=[];
  for(const second of [0.25,1.5]){
    const frame=join(dir,'frame-'+second+'.png'),decode=spawnSync('ffmpeg',['-v','error','-ss',String(second),'-i',output,'-frames:v','1','-y',frame],{encoding:'utf8',timeout:30000});
    assert.equal(decode.status,0,decode.stderr);
    frameHashes.push(createHash('sha256').update(readFileSync(frame)).digest('hex'));
  }
  assert.notEqual(frameHashes[0],frameHashes[1],'animation sample frames are identical');
  const audioCheck=spawnSync('ffmpeg',['-hide_banner','-i',output,'-vn','-af','volumedetect','-f','null','-'],{encoding:'utf8',timeout:30000});
  assert.equal(audioCheck.status,0,audioCheck.stderr);
  const peak=audioCheck.stderr.match(/max_volume:\s*(-?[\d.]+) dB/);assert.ok(peak&&Number(peak[1])>-80,'decoded test audio is silent');
  console.log(JSON.stringify({status:'passed',projectId:p.id,renderId:job.id,engine:rendered.engine,output,width:video.width,height:video.height,duration:probe.format.duration,hasAudio:true,frameHashes,audioPeakDB:Number(peak[1]),modelTested:false,visualReviewRequired:true},null,2));
} finally {rmSync(dir,{recursive:true,force:true});}
