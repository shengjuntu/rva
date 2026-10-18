// Isolated local browser + real HyperFrames/FFmpeg acceptance. No model calls.
const {spawn, execFileSync} = require('node:child_process');
const fs = require('node:fs'), path = require('node:path'), os = require('node:os');
const assert = require('node:assert/strict');
const {chromium} = require(process.env.PLAYWRIGHT_MODULE || 'playwright');
const root = fs.mkdtempSync(path.join(os.tmpdir(),'video-subtitles-'));
const app = path.resolve(__dirname,'..');
const port = Number(process.env.VIDEO_TEST_PORT || 3342), base = `http://127.0.0.1:${port}`;
const chrome = process.env.HYPERFRAMES_BROWSER_PATH, font = process.env.VIDEO_FONT;
if (!chrome || !font) throw new Error('Set HYPERFRAMES_BROWSER_PATH and VIDEO_FONT');
const env = {...process.env, VIDEO_APP_TOKEN:'subtitle-test-local-token-12345678', HYPERFRAMES_NO_TELEMETRY:'1', DO_NOT_TRACK:'1'};
const output = process.env.VIDEO_TEST_OUTPUT || root;
fs.mkdirSync(output,{recursive:true});
const log = fs.openSync(path.join(root,'app.log'),'w');
const child = spawn(path.join(app,'bin/video-app'), ['--listen',`127.0.0.1:${port}`,'--data',path.join(root,'data'),'--hyperframes',path.join(app,'runtime/node_modules/hyperframes/bin/hyperframes.mjs'),'--font',font,'--gsap',path.join(app,'runtime/node_modules/gsap/dist/gsap.min.js'),'--espeak',process.env.VIDEO_TEST_ESPEAK || ''],{env,stdio:['ignore',log,log]});
const delay = ms => new Promise(r=>setTimeout(r,ms));
const checks=[], errors=[];
async function req(url,body,method) {
 const r=await fetch(base+url,{method:method || (body ? 'POST':'GET'), headers:{Authorization:'Bearer '+env.VIDEO_APP_TOKEN,'Content-Type':'application/json'},body:body?JSON.stringify(body):undefined});
 const data=await r.json(); if(!r.ok) throw new Error(r.status+' '+JSON.stringify(data)); return data;
}
async function until(fn,timeout=15000) {
 const deadline=Date.now()+timeout;
 for (;;) { const value=await fn(); if(value)return value; assert(Date.now()<deadline,'Timed out; artifacts '+root); await delay(150); }
}
(async()=>{
 let browser;
 try {
  await until(async()=>{try {await req('/api/projects');return true;}catch{return false;}});
  browser=await chromium.launch({executablePath:chrome,headless:true,args:['--no-sandbox']});
  const page=await browser.newPage({viewport:{width:1440,height:1100}});
  page.on('pageerror', e=>errors.push(e.message));
  page.on('dialog', d=>d.accept());
  await page.goto(base); await page.locator('#token').fill(env.VIDEO_APP_TOKEN); await page.locator('#login-form button').click();
  await page.locator('#new-dialog').waitFor({state:'visible'}); await page.locator('#cancel-new').click();
  await page.locator('#news-package').setInputFiles(path.join(app,'examples/script_bundle.zip'));
  await page.locator('#news-panel').waitFor({state:'visible'});
  let p=await until(async()=>{const ps=await req('/api/projects'); return ps[0];});
  const route='/api/projects/'+p.id;
  await page.locator('#news-video-package').setInputFiles(path.join(app,'examples/video_bundle.zip'));
  await page.locator('#news-video').waitFor({state:'visible'});
  const source=(await req(route)).news;
  await page.locator('#adopt-news-audio').click();
  await until(async()=>{p=await req(route);return p.subtitles?.status==='ready';});
  await until(async()=> (await page.locator('#subtitle-state').textContent()).includes('可渲染'));
  assert.equal(p.subtitles.binding.method,'news_package');
  assert.equal(p.renders.length,0); assert.deepEqual(p.news,source);
  assert(Math.abs(p.shots.reduce((n,s)=>n+s.duration,0)-p.subtitles.binding.audioDuration)<1/30);
  checks.push('browser_import_original_video_and_adopt_audio_srt');
  if (process.env.VIDEO_TEST_ESPEAK) {
    const priorNarration=p.audio.narration.assetId;
    await page.locator('#speech-check').click();
    await until(async()=> (await page.locator('#speech-environment').textContent()).includes('已找到'));
    await page.locator('#speech-start').click();
    const speech=await until(async()=>{
      p=await req(route);const j=p.speech?.at(-1);if(!j)return false;
      if(['failed','interrupted'].includes(j.state))throw new Error(JSON.stringify(j));
      return j.state==='completed' && j;
    },45000);
    assert.equal(p.audio.narration.assetId,priorNarration);
    await page.locator('#speech-jobs button').filter({hasText:'采用配音与字幕'}).click();
    await until(async()=>{p=await req(route);return p.audio.narration.assetId===speech.id;});
    assert.equal(p.subtitles.binding.method,'synthesized_sentence_durations');assert.equal(p.subtitles.status,'ready');
    assert.deepEqual(p.news,source);
    assert.equal(p.shots[0].duration,speech.duration);
    const wav=await fetch(base+route+'/speech/'+speech.id+'/files/narration.wav',{headers:{Authorization:'Bearer '+env.VIDEO_APP_TOKEN}});
    assert.equal(wav.status,200);fs.writeFileSync(path.join(output,'tts-narration.wav'),Buffer.from(await wav.arrayBuffer()));
    await page.locator('#speech-section').screenshot({path:path.join(output,'speech-editor.png')});
    checks.push('browser_local_tts_generation_without_overwrite','browser_explicit_tts_adoption_with_measured_shots','generated_wav_download');
  }
  await page.locator('#subtitle-seek').fill('4');
  await page.locator('#subtitle-seek').dispatchEvent('input');
  assert((await page.locator('#subtitle-live').textContent()).includes('系统已完成'));
  const srt=await page.locator('#subtitle-srt').inputValue();
  await page.locator('#subtitle-section').screenshot({path:path.join(output,'subtitle-editor.png')});
  checks.push('subtitle_scrub_and_live_preview');
  await page.locator('#render').click();
  console.log('Rendering actual 1080x1920 captioned video; work:',root);
  const job=await until(async()=>{
   p=await req(route); const job=p.renders.at(-1); if(!job)return false;
   if(['failed','interrupted'].includes(job.state))throw new Error(JSON.stringify(job));
   return job.state==='completed' && job;
  },240000);
  const rendered=path.join(output,'subtitled-app.mp4');
  const download=await fetch(base+route+'/renders/'+job.id+'/final.mp4',{headers:{Authorization:'Bearer '+env.VIDEO_APP_TOKEN}});
  assert.equal(download.status,200); fs.writeFileSync(rendered,Buffer.from(await download.arrayBuffer()));
  const probe=JSON.parse(execFileSync('ffprobe',['-v','error','-show_format','-show_streams','-of','json',rendered],{encoding:'utf8'}));
  const vs=probe.streams.find(s=>s.codec_type==='video'), as=probe.streams.find(s=>s.codec_type==='audio');
  assert.equal(vs.width,1080);assert.equal(vs.height,1920);assert.equal(vs.r_frame_rate,'30/1'); assert(as);
  assert(Math.abs(Number(vs.duration)-job.duration)<.04);assert(Math.abs(Number(as.duration)-job.duration)<.04);
  const savedSRT=await fetch(base+route+'/renders/'+job.id+'/subtitles.srt',{headers:{Authorization:'Bearer '+env.VIDEO_APP_TOKEN}});
  assert.equal((await savedSRT.text()).trim(),srt.trim());
  fs.writeFileSync(path.join(output,'ffprobe.json'),JSON.stringify(probe,null,2));
  for (const [name,time] of [['first',1],['second',4]])execFileSync('ffmpeg',['-v','error','-ss',String(time),'-i',rendered,'-frames:v','1',path.join(output,name+'.png')]);
  checks.push('real_hyperframes_ffmpeg_1080x1920_30fps_audio_captions','render_snapshot_srt_preserved');
  const originalText=p.shots[0].text;
  await page.locator('#shot-text').fill(originalText+'改稿。');await page.locator('#save').click();
  await until(async()=> (await req(route)).subtitles.status==='stale');
  const count=(await req(route)).renders.length;
  await page.locator('#render').click();
  await until(async()=> (await page.locator('#toast').textContent()).includes('正文'));
  assert.equal((await req(route)).renders.length,count);
  await page.locator('#shot-text').fill(originalText);await page.locator('#save').click();
  await until(async()=> (await req(route)).subtitles.status==='ready');
  checks.push('edited_script_blocks_render_and_restore_recovers_binding');
  await page.locator('#subtitle-srt').fill(srt.replace('这是','这里是'));await page.locator('#save-subtitles').click();
  await until(async()=> (await req(route)).subtitles.status==='unreviewed');
  await page.locator('#subtitle-srt').fill(srt);await page.locator('#save-subtitles').click();
  await until(async()=> (await page.locator('#save-state').textContent()).includes('已保存'));
  // This explicit test action exercises the UI confirmation, not ASR validation.
  await page.locator('#review-subtitles').click();
  await until(async()=> (await req(route)).subtitles.status==='ready');
  assert.equal((await req(route)).subtitles.binding.method,'manual_review');
  checks.push('srt_edits_require_explicit_human_review');
  const draft=srt.replace('这是','未保存的这是');await page.locator('#subtitle-srt').fill(draft);
  p=await req(route);p.brief+='\nAgent concurrent edit'; await req(route,p,'PUT');
  await page.locator('#conflict').waitFor({state:'visible'});
  assert.equal(await page.locator('#subtitle-srt').inputValue(),draft);
  await page.locator('#save-subtitles').click();
  await until(async()=> (await page.locator('#toast').textContent()).includes('其他操作'));
  assert.equal(await page.locator('#subtitle-srt').inputValue(),draft);
  checks.push('concurrent_agent_edit_keeps_unsaved_srt_and_rejects_stale_save');
  await page.locator('#reload-project').click();
  await until(async()=> (await page.locator('#save-state').textContent()).includes('已保存'));
  assert.deepEqual((await req(route)).news,source); assert.deepEqual(errors,[]);
  checks.push('source_package_unchanged','no_browser_javascript_errors');
  const report={status:'passed',checks,work_dir:root,project_id:p.id,render_id:job.id,duration:job.duration,browser:await browser.version(),limitations:['RunDesk/real Codex model not exercised by this test','Caption review confirmation is exercised mechanically, not an ASR accuracy test']};
  fs.writeFileSync(path.join(output,'subtitle-report.json'),JSON.stringify(report,null,2));console.log(JSON.stringify(report,null,2));
 } finally {
  if(browser)await browser.close();child.kill('SIGTERM');
  await Promise.race([new Promise(r=>child.once('exit',r)),delay(8000)]);if(child.exitCode===null)child.kill('SIGKILL');fs.closeSync(log);
 }
})().catch(e=>{console.error(e);process.exitCode=1;});
