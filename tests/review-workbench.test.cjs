const test = require('node:test');
const assert = require('node:assert/strict');
const {JSDOM} = require('jsdom');
const fs = require('node:fs'), vm = require('node:vm'), path = require('node:path');
const root = path.resolve(__dirname, '..');

function fixture() {
  const dom = new JSDOM(fs.readFileSync(path.join(root, 'web/index.html'), 'utf8'), {url:'http://127.0.0.1:3000', runScripts:'outside-only'});
  const w = dom.window;
  w.HTMLMediaElement.prototype.pause = function() {};
  w.HTMLMediaElement.prototype.load = function() {};
  const ctx = dom.getInternalVMContext();
  vm.runInContext(`
    let project = {id:'test', revision:7, narrationText:'当前权威正文。', workflow:{editingBackend:'hyperframes'}, renders:[{id:'done',state:'completed',revision:6,engine:'hyperframes',duration:8},{id:'running',state:'running',revision:7},{id:'range',state:'completed',revision:5,range:{start:3,end:5}}]};
    let epoch=1, narrationDirty=false, productionDirty=false, dirty=false, editDSLDirty=false;
    const $=s=>document.querySelector(s);
    const el=(tag,text,cls)=>{const n=document.createElement(tag);if(text!==undefined)n.textContent=text;if(cls)n.className=cls;return n};
    function action(fn){return fn;}
    function editingBackendLabel(backend){return backend==='hyperframes'?'HyperFrames':backend==='video_use'?'video-use':'内置剪辑';}
    let calls=[];
    async function api(url){calls.push(url);return globalThis.responseFixture;}
  `, ctx);
  vm.runInContext(fs.readFileSync(path.join(root, 'web/review.js'), 'utf8'), ctx);
  vm.runInContext('initReviewTimeline();resetReviewTimeline()', ctx);
  return {dom,w,ctx,run:code=>vm.runInContext(code,ctx)};
}

const current = {projectId:'test',revision:7,backend:'hyperframes',current:true,source:'authored',sourceHash:'saved-source',duration:8,warnings:['声明的时间线尚未逐帧验证。'],tracks:[{id:'picture',kind:'animation',label:'新闻画面',clips:[{id:'scene1',start:1,end:4,label:'<img src=x onerror=alert(1)>',assetId:'asset1',textRefs:[{quote:'当前权威正文。'}]}]}]};

test('review exposes four readonly tracks, safe authored text, accurate seek and non-destructive chat references', () => {
  const f=fixture();try {
    f.run('drawReviewTimeline('+JSON.stringify(current)+')');
    const $=s=>f.w.document.querySelector(s);
    assert.equal(f.w.document.querySelectorAll('.review-lane').length,4);
    assert.equal(f.w.document.querySelectorAll('.review-block').length,1);
    assert.equal($('#review-timeline img'),null);
    assert.match($('#review-status').textContent,/Agent 声明/);
    assert.equal($('#review-player').hidden,true);
    assert.match($('#review-no-video').textContent,/尚无对应/);
    assert.match($('#review-warnings').textContent,/未逐帧/);
    assert.equal(f.w.document.querySelectorAll('.review-lane-empty').length,3);
    $('#prompt').value='尚未发送的修改';
    $('.review-block').click();
    assert.match($('#review-position').textContent,/00:01.000/);
    assert.match($('#review-selection').textContent,/当前权威正文/);
    assert.equal($('#review-text mark').textContent,'当前权威正文。');
    $('#review-selection button').click();
    assert.match($('#prompt').value,/^尚未发送的修改\n\n/);
    assert.match($('#prompt').value,/saved-source/);
    assert.match($('#prompt').value,/片段 ID：scene1/);
    assert.match($('#review-status').textContent,/尚未执行/);
    assert.equal(f.run('calls.length'),0);
    $('#review-zoom').value='3';$('#review-zoom').dispatchEvent(new f.w.Event('input'));
    assert.equal($('#review-timeline').style.minWidth,'300%');
    assert.equal($('.review-block').draggable,false);
  } finally {f.dom.window.close();}
});

test('historical render never substitutes current text and identifies its snapshot in adjustments', () => {
  const f=fixture();try {
    f.run('drawReviewVersions()');
    assert.equal(f.w.document.querySelectorAll('#review-version option').length,3);
    const data={...current,revision:6,renderId:'done',current:false};
    f.run('drawReviewTimeline('+JSON.stringify(data)+')');
    assert.equal(f.w.document.querySelector('#review-player').hidden,false);
    assert.match(f.w.document.querySelector('#review-player').src,/\/renders\/done\/final.mp4$/);
    assert.doesNotMatch(f.w.document.querySelector('#review-text').textContent,/当前权威正文/);
    assert.match(f.w.document.querySelector('#review-text').textContent,/历史快照未提供正文/);
    f.run('appendReviewReference({quote:"历史正文"})');
    assert.match(f.w.document.querySelector('#prompt').value,/历史作品审阅/);
    assert.match(f.w.document.querySelector('#prompt').value,/不要覆盖较新的修改/);
    assert.throws(()=>f.run('drawReviewTimeline({projectId:"other",revision:7,tracks:[]})'),/不属于/);
    f.run('drawReviewTimeline('+JSON.stringify({...current,revision:6})+')');
    assert.throws(()=>f.run('appendReviewReference({quote:"stale"})'),/已更新/);
    f.run('drawReviewTimeline('+JSON.stringify({...current,current:false})+')');
    assert.match(f.w.document.querySelector('#review-status').textContent,/当前工程（需核对）/);
    assert.throws(()=>f.run('appendReviewReference({quote:"not verified"})'),/尚未核对/);
    f.run('narrationDirty=true');
    assert.throws(()=>f.run('appendReviewReference({quote:"dirty"})'),/未保存/);
  } finally {f.dom.window.close();}
});

test('HyperFrames source is plain text and missing or invalid track timing stays explicit', () => {
  const f=fixture();try {
    f.run('drawHyperframesProject('+JSON.stringify({projectId:'test',revision:7,current:true,document:{entrypoint:'index.html',canvas:{width:1080,height:1920,fps:30,duration:8},files:[{path:'index.html',content:'<script>window.pwned=true</script><img src=x onerror=alert(1)>'}]},assets:[{assetId:'asset1',path:'assets/source.png'}]})+')');
    assert.equal(f.w.document.querySelector('#hyperframes-project').hidden,false);
    assert.equal(f.w.document.querySelector('#hyperframes-files').querySelector('script,img'),null);
    assert.match(f.w.document.querySelector('#hyperframes-files pre').textContent,/<script>/);
    assert.equal(f.w.pwned,undefined);
    f.run('drawReviewTimeline('+JSON.stringify({...current,duration:0,tracks:[{id:'bad',kind:'visual',clips:[{id:'negative',start:-3,end:2}]}]})+')');
    assert.equal(f.w.document.querySelectorAll('.review-block').length,0);
    assert.equal(f.w.document.querySelector('#review-seek').disabled,true);
    assert.match(f.w.document.querySelector('#review-timeline').textContent,/未提供可用时长/);
  } finally {f.dom.window.close();}
});

test('text selection appends an exact excerpt and repeated references highlight only an explicit occurrence', () => {
  const f=fixture();try {
    f.run('drawReviewTimeline('+JSON.stringify({...current,text:'同一句。🐈同一句。'})+')');
    f.run('drawReviewText([{quote:"同一句。"}])');
    assert.equal(f.w.document.querySelectorAll('#review-text mark').length,0);
    f.run('drawReviewText([{quote:"同一句。",startRune:5,endRune:9}])');
    assert.equal(f.w.document.querySelectorAll('#review-text mark').length,1);
    assert.equal(f.w.document.querySelector('#review-text mark').previousSibling.textContent,'同一句。🐈');
    const text=f.w.document.querySelector('#review-text mark').firstChild;
    const range=f.w.document.createRange();range.setStart(text,0);range.setEnd(text,3);
    const selection=f.w.getSelection();selection.removeAllRanges();selection.addRange(range);
    f.w.document.querySelector('#review-text').dispatchEvent(new f.w.Event('mouseup'));
    assert.equal(f.w.document.querySelector('#review-select-text').disabled,false);
    f.w.document.querySelector('#review-select-text').click();
    assert.match(f.w.document.querySelector('#prompt').value,/所选原文：同一句/);
  } finally {f.dom.window.close();}
});

test('range preview uses project-time coordinates and identifies positions outside its playable window', () => {
  const f=fixture();try {
    f.run('drawReviewTimeline('+JSON.stringify({...current,renderId:'range',current:false})+')');
    f.run('seekReviewPosition(4)');
    assert.equal(f.w.document.querySelector('#review-player').currentTime,1);
    f.run('seekReviewPosition(7)');
    assert.equal(f.w.document.querySelector('#review-player').currentTime,1);
    assert.match(f.w.document.querySelector('#review-status').textContent,/不在此局部预览/);
  } finally {f.dom.window.close();}
});

test('review response for an old project is ignored after a project switch', async () => {
  const f=fixture();try {
    f.run('globalThis.responseFixture='+JSON.stringify(current));
    await f.run('loadReviewTimeline(true)');
    assert.match(f.w.document.querySelector('#review-status').textContent,/HyperFrames/);
    assert.match(f.run('calls[0]'),/review-timeline/);
    f.run('let releaseReview; api=async()=>await new Promise(resolve=>releaseReview=resolve);globalThis.oldLoad=loadReviewTimeline(true);epoch++;project={...project,id:"new"};resetReviewTimeline();releaseReview('+JSON.stringify(current)+')');
    await f.run('oldLoad');
    assert.equal(f.w.document.querySelectorAll('.review-block').length,0);
    assert.equal(f.run('reviewData'),null);
  } finally {f.dom.window.close();}
});
