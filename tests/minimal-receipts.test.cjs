'use strict';

const {test} = require('node:test');
const assert = require('node:assert/strict');
const {JSDOM} = require('jsdom');
const fs = require('node:fs'), vm = require('node:vm'), path = require('node:path');
const root = path.resolve(__dirname, '..');
const projectID = 'a'.repeat(24), requestA = 'b'.repeat(24), requestB = 'c'.repeat(24);
const bodyA = {text: '第一次制作', requestId: requestA, skills: ['video-review']};
const bodyB = {text: '后来的独立修改', requestId: requestB, skills: ['video-editing']};

function fixture() {
  const dom = new JSDOM(fs.readFileSync(path.join(root, 'web/index.html'), 'utf8'), {url: 'http://127.0.0.1:3220', runScripts: 'outside-only'});
  const w = dom.window, ctx = dom.getInternalVMContext(), calls = [];
  w.HTMLMediaElement.prototype.pause = function () {};
  w.HTMLMediaElement.prototype.load = function () {};
  w.FormData = FormData;
  const source = fs.readFileSync(path.join(root, 'web/app.js'), 'utf8');
  const boot = source.lastIndexOf('\nboot().catch(');
  assert.ok(boot > 0, 'controller fixture must suppress only automatic startup');
  vm.runInContext(source.slice(0, boot), ctx);
  vm.runInContext(`project={id:${JSON.stringify(projectID)},revision:7,name:'回执测试',narrationText:'保存的正文。',workflow:{editingBackend:'hyperframes',skills:['video-narration','video-editing','video-review']},assets:[],renders:[],sessionId:'session'};`, ctx);
  w.fetch = async (route, options = {}) => {
    calls.push({route: String(route), options});
    const pathname = new URL(route, w.location.href).pathname;
    const response = pathname.endsWith('/production-board') ? {projectId: projectID, stages: []} : pathname.endsWith('/jobs') ? {jobs: []} : {};
    return new Response(JSON.stringify(response), {headers: {'Content-Type': 'application/json'}});
  };
  return {w, dom, calls, run: code => vm.runInContext(code, ctx), plain: code => JSON.parse(vm.runInContext('JSON.stringify(' + code + ')', ctx)), close: async () => { await new Promise(resolve => setImmediate(resolve)); dom.window.close(); }};
}

test('malformed durable receipt blocks fresh dispatch while completed videos remain viewable', async () => {
  const f = fixture();
  try {
    f.w.localStorage.setItem('video-message:' + projectID, '{broken JSON');
    f.run(`project.renders=[{id:'${'d'.repeat(24)}',state:'completed',revision:7,duration:3}];drawProject()`);
    assert.equal(f.w.document.querySelector('#output-player').hidden, false);
    assert.match(f.w.document.querySelector('#output-download').href, /\/renders\/[a-f0-9]{24}\/final\.mp4/);
    assert.equal(f.w.document.querySelector('#draft-start').disabled, true);
    assert.equal(f.w.document.querySelector('#send').disabled, true);
    assert.match(f.w.document.querySelector('#workflow-status').textContent, /无法读取|核对/);
    try { await f.run('startProduction()'); } catch {}
    assert.equal(f.calls.filter(call => call.route.endsWith('/messages')).length, 0);
    assert.equal(f.w.localStorage.getItem('video-message:' + projectID), '{broken JSON');
  } finally { await f.close(); }
});

test('server pending recovery retains original skills and expected turn identity', async () => {
  const f = fixture();
  try {
    const submission = {id: requestA, state: 'pending', text: '已经在服务端登记的制作', skills: ['video-review'], turnId: 'original-turn'};
    f.run('reconcileMessage(' + JSON.stringify(submission) + ')');
    assert.deepEqual(f.plain('pendingMessage(project.id)'), {text: submission.text, requestId: requestA, skills: submission.skills, expectedTurnId: 'original-turn'});
    assert.deepEqual(JSON.parse(f.w.localStorage.getItem('video-message:' + projectID)), f.plain('pendingMessage(project.id)'));
  } finally { await f.close(); }
});

test('late acknowledgement of request A cannot erase a newer request B receipt', async () => {
  const f = fixture();
  try {
    let release;
    f.w.fetch = async () => await new Promise(resolve => { release = resolve; });
    f.run('rememberMessage(project.id,' + JSON.stringify(bodyA) + ')');
    const oldDelivery = f.run('deliverMessage(project.id,' + JSON.stringify(bodyA) + ')');
    f.run('rememberMessage(project.id,' + JSON.stringify(bodyB) + ')');
    release(new Response('{}', {status: 200, headers: {'Content-Type': 'application/json'}}));
    await oldDelivery;
    assert.deepEqual(f.plain('pendingMessage(project.id)'), bodyB);
  } finally { await f.close(); }
});

test('authentication failures retain uncertain receipts; only an explicit rejection clears the matching request', async () => {
  for (const status of [401, 403]) {
    const f = fixture();
    try {
      f.run('rememberMessage(project.id,' + JSON.stringify(bodyA) + ')');
      f.w.fetch = async () => new Response(JSON.stringify({error: 'authentication required'}), {status, headers: {'Content-Type': 'application/json'}});
      await assert.rejects(f.run('deliverMessage(project.id,' + JSON.stringify(bodyA) + ')'), /authentication/);
      assert.deepEqual(f.plain('pendingMessage(project.id)'), bodyA);
    } finally { await f.close(); }
  }
  const f = fixture();
  try {
    f.run('rememberMessage(project.id,' + JSON.stringify(bodyA) + ')');
    f.w.fetch = async () => new Response(JSON.stringify({error: 'explicitly rejected', code: 'submission_rejected'}), {status: 409, headers: {'Content-Type': 'application/json'}});
    await assert.rejects(f.run('deliverMessage(project.id,' + JSON.stringify(bodyA) + ')'), /explicitly rejected/);
    assert.equal(f.run('pendingMessage(project.id)'), null);
  } finally { await f.close(); }
});

test('local stop blocks new submissions and cancels only jobs captured before stopping', async () => {
  const f = fixture();
  try {
    const oldJob = 'render:' + 'd'.repeat(24), newJob = 'render:' + 'e'.repeat(24);
    let releaseStop, captureStarted, agentStopStarted, reads = 0, messageCount = 0;
    const captured = new Promise(resolve => { captureStarted = resolve; });
    const stopping = new Promise(resolve => { agentStopStarted = resolve; });
    const stopGate = new Promise(resolve => { releaseStop = resolve; });
    const cancelled = [];
    f.w.document.querySelector('#prompt').value = '停止尚未完成时不得发送';
    f.w.fetch = async (route, options = {}) => {
      const pathname = new URL(route, f.w.location.href).pathname;
      if (pathname.endsWith('/agent/stop')) { agentStopStarted(); await stopGate; }
      if (pathname.endsWith('/jobs')) {
        reads++; captureStarted();
        return new Response(JSON.stringify({jobs: [{jobId: reads === 1 ? oldJob : newJob, state: 'running'}]}), {headers: {'Content-Type': 'application/json'}});
      }
      if (pathname.endsWith('/cancel')) cancelled.push(decodeURIComponent(pathname.split('/').at(-2)));
      if (pathname.endsWith('/messages')) messageCount++;
      return new Response('{}', {headers: {'Content-Type': 'application/json'}});
    };
    const stop = f.run('stopProduction()');
    await Promise.race([captured, new Promise((_, reject) => setTimeout(() => reject(Error('stop must capture jobs before waiting on agent cancellation')), 1000))]);
    await stopping;
    assert.equal(f.w.document.querySelector('#draft-start').disabled, true);
    assert.equal(f.w.document.querySelector('#send').disabled, true);
    await f.run('startProduction()');
    await f.run('submitMessage({preventDefault(){}})');
    assert.equal(messageCount, 0);
    releaseStop(); await stop;
    assert.deepEqual(cancelled, [oldJob]);
    assert.ok(!cancelled.includes(newJob));
  } finally { await f.close(); }
});
