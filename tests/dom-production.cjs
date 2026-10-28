'use strict';

const assert = require('node:assert/strict');
const {JSDOM} = require('jsdom');
const {File} = require('node:buffer');
const {spawn} = require('node:child_process');
const fs = require('node:fs'), vm = require('node:vm'), path = require('node:path'), os = require('node:os');
const backendCompatibility = require('./backend-compatibility.cjs');
const root = path.resolve(__dirname, '..');
const port = process.env.VIDEO_TEST_PORT || '3321';
const base = 'http://127.0.0.1:' + port, token = 'dom-test-token-not-secret';
const data = fs.mkdtempSync(path.join(os.tmpdir(), 'video-minimal-dom-'));
const binary = process.env.VIDEO_TEST_BIN || path.join(root, 'bin/video-app');
const checks = [];
const removed = ['advanced-workspace','speech-section','speech-start','speech-adopt','speech-check','render','range-render','edit-panel','edit-json','edit-save','edit-example','edit-patch','patch-apply','patch-undo','alignment-table','production-panel','workflow-backend','workflow-save','skill-narration','skill-editing','skill-review','runtime-diagnostics','locate-clips','locate-results','review-timeline','review-zoom','review-selection','review-select-text','hyperframes-project','hyperframes-files','tts-settings','connect','refresh-tools','new-session'];

(async () => {
  const proc = spawn(binary, ['--data', data, '--listen', '127.0.0.1:' + port], {env: {...process.env, VIDEO_APP_TOKEN: token}});
  const realFetch = (route, options = {}) => fetch(new URL(route, base), {...options, headers: {...options.headers, Authorization: 'Bearer ' + token}});
  let ui;
  try {
    await new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(Error('server startup timeout')), 10000);
      proc.stderr.on('data', raw => { if (raw.toString().includes(base)) { clearTimeout(timer); resolve(); } });
      proc.on('error', error => { clearTimeout(timer); reject(error); });
      proc.on('exit', code => { clearTimeout(timer); reject(Error('server exited ' + code)); });
    });
    const health = await (await realFetch('/api/health')).json();
    assert.equal(health.version, '0.28.0', 'run this regression against the newly built release binary');

    const transport = {connected: new Set(), messages: [], events: new Map(), orders: [], loseNext: true, connectCount: 0, uploadGate: null};
    async function uiFetch(route, options = {}) {
      const url = new URL(route, base), method = options.method || 'GET';
      transport.orders.push(method + ' ' + url.pathname);
      const match = url.pathname.match(/^\/api\/projects\/([a-f0-9]{24})(.*)$/);
      if (url.pathname === '/api/runtime-profiles') return json({defaultProfile:'',rundeskURL:'http://127.0.0.1:3210',warnings:[],items:[{id:'',name:'原默认环境',description:'测试用已配置环境',enabled:true,isDefault:true,state:'not_started',selectable:true}]});
      if (match) {
        const pid = match[1], tail = match[2];
        if (tail === '/assets' && method === 'POST' && transport.uploadGate) {
          const gate = transport.uploadGate; transport.uploadGate = null;
          await gate;
        }
        if (tail === '/connect') {
          transport.connected.add(pid); transport.connectCount++;
          const saved = await (await realFetch('/api/projects/' + pid)).json();
          return json({...saved, sessionId: 'test-session-' + pid, workspaceId: 'test-workspace', toolsVersion: '0.28.0'});
        }
        if (tail === '/agent') return json({status: 'idle', appRunId: 'fixture-run'});
        if (tail === '/events') return json((transport.events.get(pid) || []).filter(event => event.id > Number(url.searchParams.get('after') || 0)));
        if (tail === '/approvals') return json([]);
        if (tail === '/messages') {
          const body = JSON.parse(options.body); transport.messages.push(body);
          const events = transport.events.get(pid) || [];
          if (!events.some(event => event.requestId === body.requestId)) {
            const first = events.length + 1;
            events.push({id: first, requestId: body.requestId, method: 'run/input', data: {input: {text: body.text}}}, {id: first + 1, requestId: body.requestId, direction: 'in', method: 'item/completed', data: {params: {turnId: body.requestId, item: {id: 'answer', type: 'agentMessage', text: '已收到修改，制作完成后会提供成片。'}}}}, {id: first + 2, requestId: body.requestId, direction: 'in', method: 'item/completed', data: {params: {turnId: body.requestId, item: {id: 'technical', type: 'mcpToolCall', arguments: 'raw-debug-arguments-should-not-appear'}}}});
            transport.events.set(pid, events);
          }
          if (transport.loseNext) { transport.loseNext = false; throw new TypeError('simulated response lost after admission'); }
          return json({requestId: body.requestId, state: 'accepted'});
        }
        if (tail === '' && method === 'GET' && transport.connected.has(pid)) {
          const saved = await (await realFetch(route, options)).json();
          return json({...saved, sessionId: 'test-session-' + pid, workspaceId: 'test-workspace', toolsVersion: '0.28.0'});
        }
      }
      return realFetch(route, options);
    }
    function json(value) { return new Response(JSON.stringify(value), {status: 200, headers: {'Content-Type': 'application/json'}}); }
    const storageSnapshot = window => Object.fromEntries(Array.from({length: window.localStorage.length}, (_, i) => window.localStorage.key(i)).map(key => [key, window.localStorage.getItem(key)]));
    async function loadUI(storage = {}) {
      const html = await (await realFetch('/')).text();
      const dom = new JSDOM(html, {url: base, runScripts: 'outside-only'}), w = dom.window, ctx = dom.getInternalVMContext();
      for (const [key, value] of Object.entries(storage)) w.localStorage.setItem(key, value);
      w.fetch = uiFetch; w.FormData = FormData; w.AbortController = AbortController; w.confirm = () => true;
      w.HTMLMediaElement.prototype.pause = function () {}; w.HTMLMediaElement.prototype.load = function () {};
      w.HTMLDialogElement.prototype.showModal = function () { this.open = true; }; w.HTMLDialogElement.prototype.close = function () { this.open = false; };
      w.setInterval = () => 0;
      for (const script of [...w.document.querySelectorAll('script[src]')]) {
        const source = await (await realFetch(script.getAttribute('src'))).text();
        if (script.getAttribute('src') === '/app.js') {
          assert.ok(/boot\(\)\.catch\(/.test(source), 'workbench boot must execute in the regression');
          vm.runInContext(source.replace(/boot\(\)\.catch\(/, 'globalThis.initialBoot = boot(); initialBoot.catch('), ctx);
          await vm.runInContext('initialBoot', ctx);
        } else vm.runInContext(source, ctx);
      }
      return {dom, w, ctx, run: code => vm.runInContext(code, ctx), $: selector => w.document.querySelector(selector)};
    }
    function input(selector, value) { ui.$(selector).value = value; ui.$(selector).dispatchEvent(new ui.w.Event('input')); }
    function files(selector, values) { Object.defineProperty(ui.$(selector), 'files', {configurable: true, value: values}); }
    async function project(pid) { return (await realFetch('/api/projects/' + pid)).json(); }

    ui = await loadUI();
    assert.equal(ui.run('project'), null);
    assert.equal((await (await realFetch('/api/projects')).json()).length, 0);
    assert.equal(ui.$('#new-dialog').open, true);
    assert.equal(ui.$('#new-profile').value, '');
    await ui.$('#new-form').onsubmit({preventDefault(){}});
    const pid = ui.run('project.id');
    assert.match(pid, /^[a-f0-9]{24}$/);
    assert.equal((await (await realFetch('/api/projects')).json()).length, 1);
    assert.equal(ui.$('#new-dialog').open, false);
    checks.push('first launch chooses the listed default environment before explicitly creating one empty project');
    for (const id of removed) assert.equal(ui.w.document.getElementById(id), null, id + ' must be removed, not folded or hidden');
    assert.equal(ui.w.document.querySelector('script[src="/review.js"]'), null);
    assert.equal(ui.w.document.querySelectorAll('#app input[type=range]').length, 0);
    assert.equal(new URL(ui.$('#settings-link').href).pathname, '/settings.html');
    assert.equal(ui.$('#tts-url'), null);
    checks.push('manual TTS, render, DSL, alignment, multitrack, source-code and diagnostics controls are absent from the DOM');
    checks.push('configuration is reachable through a separate settings page');

    const image = fs.readFileSync(path.join(root, 'docs/preview.png'));
    files('#folder-files', [new File(['从材料目录导入的正文。'], 'script.md', {type: 'text/plain'}), new File([image], 'news.png', {type: 'image/png'})]);
    await ui.$('#folder-files').onchange();
    assert.equal(ui.$('#narration-text').value, '从材料目录导入的正文。');
    assert.equal((await project(pid)).narrationText || '', '');
    assert.equal((await project(pid)).assets.length, 2);
    checks.push('a material directory with exactly one TXT or Markdown adopts it as an unsaved script only when narration is empty');

    input('#narration-text', '第一条自由正文。素材必须服从这段文字。');
    input('#workflow-instructions', '保持原文，先做初稿');
    input('#prompt', '尚未发送的调整草稿');
    files('#upload', [new File(['不应覆盖已有正文。'], 'do-not-replace.txt', {type: 'text/plain'})]);
    await ui.$('#upload').onchange();
    assert.equal(ui.$('#narration-text').value, '第一条自由正文。素材必须服从这段文字。');
    let releaseUpload;
    transport.uploadGate = new Promise(resolve => { releaseUpload = resolve; });
    files('#folder-files', [new File(['不得覆盖正文甲。'], 'a.txt', {type: 'text/plain'}), new File(['不得覆盖正文乙。'], 'b.md', {type: 'text/plain'}), new File([image], 'another.png', {type: 'image/png'})]);
    const uploading = ui.$('#folder-files').onchange();
    await new Promise(resolve => setTimeout(resolve, 0));
    assert.equal(ui.$('#draft-start').disabled, true, 'start must be unavailable while upload updates revisions');
    releaseUpload(); await uploading;
    assert.equal(ui.$('#narration-text').value, '第一条自由正文。素材必须服从这段文字。');
    assert.equal(ui.$('#workflow-instructions').value, '保持原文，先做初稿');
    assert.equal(ui.$('#prompt').value, '尚未发送的调整草稿');
    assert.equal((await project(pid)).assets.length, 6);
    await ui.run('drawProject()');
    assert.equal(ui.$('#narration-text').value, '第一条自由正文。素材必须服从这段文字。');
    checks.push('material upload disables start and preserves narration, requirements and unsent conversation while advancing revisions');
    checks.push('multiple script files and existing narration never trigger an arbitrary replacement');

    const otherResponse = await realFetch('/api/projects', {method: 'POST', headers: {'Content-Type': 'application/json'}, body: JSON.stringify({name: '另一个草稿项目'})});
    const other = await otherResponse.json();
    await ui.run('openProject(' + JSON.stringify(other.id) + ')');
    assert.equal(ui.$('#prompt').value, '');
    files('#folder-files', [new File(['候选正文甲。'], 'candidate-a.txt', {type: 'text/plain'}), new File(['候选正文乙。'], 'candidate-b.md', {type: 'text/plain'})]);
    await ui.$('#folder-files').onchange();
    assert.equal(ui.$('#narration-text').value, '', 'multiple scripts must not guess an authoring source');
    assert.equal((await project(other.id)).assets.length, 2);
    const choices = [...ui.w.document.querySelectorAll('#assets button')].filter(button => button.textContent === '作为正文');
    assert.equal(choices.length, 2);
    await choices[1].onclick();
    assert.equal(ui.$('#narration-text').value, '候选正文乙。');
    assert.equal((await project(other.id)).narrationText || '', '', 'explicit selection is a draft until production/save');
    input('#prompt', '另一个项目的独立草稿');
    await ui.run('openProject(' + JSON.stringify(pid) + ')');
    assert.equal(ui.$('#prompt').value, '尚未发送的调整草稿');
    const savedDraft = storageSnapshot(ui.w);
    assert.ok(savedDraft['video-draft-v1:' + pid]);
    ui.dom.window.close(); ui = await loadUI(savedDraft);
    await ui.run('openProject(' + JSON.stringify(pid) + ')');
    assert.equal(ui.$('#prompt').value, '尚未发送的调整草稿');
    assert.equal(ui.$('#narration-text').value, '第一条自由正文。素材必须服从这段文字。');
    assert.equal(ui.$('#workflow-instructions').value, '保持原文，先做初稿');
    assert.equal(ui.$('#prompt').value, '尚未发送的调整草稿');
    assert.equal(transport.messages.length, 0);
    checks.push('unsent conversation, narration and requirements survive project switches and reload with project isolation');

    transport.orders = [];
    await assert.rejects(ui.run('startProduction()'), /response lost/);
    const admitted = await project(pid);
    assert.equal(admitted.narrationText, '第一条自由正文。素材必须服从这段文字。');
    assert.equal(admitted.workflow.instructions, '保持原文，先做初稿');
    assert.equal(transport.messages.length, 1);
    assert.match(transport.messages[0].requestId, /^[a-f0-9]{24}$/);
    assert.ok(transport.messages[0].text.includes('保持原文，先做初稿'));
    assert.ok(!transport.messages[0].text.includes('尚未发送的调整草稿'));
    assert.equal(ui.$('#prompt').value, '尚未发送的调整草稿');
    const saveAt = transport.orders.indexOf('PUT /api/projects/' + pid + '/narration');
    const workflowAt = transport.orders.indexOf('PUT /api/projects/' + pid + '/workflow');
    const instructionAt = transport.orders.indexOf('POST /api/projects/' + pid + '/draft/instruction');
    const sendAt = transport.orders.indexOf('POST /api/projects/' + pid + '/messages');
    assert.ok(saveAt >= 0 && workflowAt > saveAt && instructionAt > workflowAt && sendAt > instructionAt, transport.orders.join('\n'));
    assert.equal(transport.connectCount, 1);
    assert.match(ui.$('#send-state').textContent, /确认|保留/);
    assert.deepEqual(JSON.parse(ui.w.localStorage.getItem('video-message:' + pid)), transport.messages[0]);
    checks.push('one click saves the latest narration and requirements before instruction, automatic connection and dispatch');
    checks.push('one-click production preserves unsent conversation drafts and does not send them as production instructions');
    checks.push('a lost dispatch response keeps the exact request in durable local storage');

    input('#prompt', '保留的下一条调整');
    const uncertainStorage = storageSnapshot(ui.w);
    ui.dom.window.close(); ui = await loadUI(uncertainStorage);
    await ui.run('openProject(' + JSON.stringify(pid) + ')');
    assert.equal(ui.$('#prompt').value, '保留的下一条调整');
    if (ui.w.localStorage.getItem('video-message:' + pid)) await ui.run('startProduction()');
    assert.ok(transport.messages.length >= 2);
    for (const replay of transport.messages.slice(1)) assert.deepEqual(replay, transport.messages[0]);
    assert.equal(transport.connectCount, 1);
    assert.equal(ui.$('#prompt').value, '保留的下一条调整');
    assert.equal(ui.w.localStorage.getItem('video-message:' + pid), null);
    await ui.run('poll()');
    assert.match(ui.$('#conversation').textContent, /已收到修改/);
    const replayStorage = storageSnapshot(ui.w);
    ui.dom.window.close(); ui = await loadUI(replayStorage);
    await ui.run('openProject(' + JSON.stringify(pid) + ')'); await ui.run('poll()');
    assert.match(ui.$('#conversation').textContent, /已收到修改/);
    assert.match(ui.$('#conversation').textContent, /生成初稿/);
    assert.ok(!ui.$('#conversation').textContent.includes('raw-debug-arguments-should-not-appear'));
    assert.equal(ui.$('#conversation pre, #conversation details'), null);
    assert.equal(ui.$('#prompt').value, '保留的下一条调整');
    checks.push('reload retries the original request ID, text and skills without erasing a newer correction');
    checks.push('formal conversation is replayed from RunDesk events after reopening instead of fabricated locally');

    input('#narration-text', '修订后的权威正文。'); input('#workflow-instructions', '修改配色但保留事实');
    await ui.run('submitMessage({preventDefault(){}})');
    const corrected = await project(pid);
    assert.equal(corrected.narrationText, '修订后的权威正文。');
    assert.equal(corrected.workflow.instructions, '修改配色但保留事实');
    assert.equal(transport.messages.length, 3);
    assert.equal(transport.messages[2].text, '保留的下一条调整');
    assert.notEqual(transport.messages[2].requestId, transport.messages[0].requestId);
    checks.push('conversation corrections save current authoring drafts before sending a fresh idempotent request');
    const beforeQuota = transport.messages.length;
    input('#prompt', '存储故障时不得发送这条消息');
    const setItem = ui.w.Storage.prototype.setItem;
    ui.w.Storage.prototype.setItem = function () { throw Error('fixture storage quota exceeded'); };
    try { await assert.rejects(ui.run('submitMessage({preventDefault(){}})'), /quota/); }
    finally { ui.w.Storage.prototype.setItem = setItem; }
    assert.equal(transport.messages.length, beforeQuota);
    assert.equal(ui.run('pendingMessage(project.id)'), null);
    checks.push('failure to persist a request receipt prevents dispatch rather than creating an unrecoverable submission');

    const staleProject = {...await project(pid), sessionId: 'test-session-' + pid, toolsVersion: '0.28.0'};
    let releaseOldGet, oldGetStarted;
    const getStarted = new Promise(resolve => { oldGetStarted = resolve; });
    const oldGet = new Promise(resolve => { releaseOldGet = resolve; });
    let holdGet = true;
    ui.w.fetch = async (route, options = {}) => {
      if (holdGet && (options.method || 'GET') === 'GET' && new URL(route, base).pathname === '/api/projects/' + pid) {
        holdGet = false; oldGetStarted(); await oldGet; return json(staleProject);
      }
      return uiFetch(route, options);
    };
    const oldPoll = ui.run('poll()'); await getStarted;
    input('#narration-text', '保存成功后不应被旧轮询覆盖的正文。');
    await ui.run('saveNarration()');
    const latest = await project(pid);
    assert.ok(latest.revision > staleProject.revision);
    releaseOldGet(); await oldPoll;
    assert.equal(ui.run('project.revision'), latest.revision);
    assert.equal(ui.$('#narration-text').value, latest.narrationText);
    ui.w.fetch = uiFetch;
    checks.push('a delayed older HTTP poll cannot roll back a newly saved narration or project revision');

    await ui.run(`globalThis.realRenders=project.renders;project.renders=[{id:'${'d'.repeat(24)}',revision:1,state:'completed',engine:'hyperframes_authored',duration:3},{id:'${'e'.repeat(24)}',revision:2,state:'completed',engine:'video_use',duration:2,range:{start:10,end:12}},{id:'${'f'.repeat(24)}',revision:3,state:'running',engine:'hyperframes_authored'}];drawOutputs()`);
    const options = [...ui.$('#output-version').options];
    assert.ok(options.some(option => option.value === 'd'.repeat(24)));
    assert.ok(options.some(option => option.value === 'e'.repeat(24) && /局部/.test(option.textContent)));
    assert.ok(!options.some(option => option.value === 'f'.repeat(24)));
    ui.$('#output-version').value = 'd'.repeat(24); ui.$('#output-version').dispatchEvent(new ui.w.Event('change'));
    assert.equal(new URL(ui.$('#output-download').href).pathname, '/api/projects/' + pid + '/renders/' + 'd'.repeat(24) + '/final.mp4');
    assert.equal(ui.$('#output-player').hidden, false);
    await ui.run('drawOutputs()');
    assert.equal(ui.$('#output-version').value, 'd'.repeat(24));
    ui.$('#output-version').value = 'e'.repeat(24); ui.$('#output-version').dispatchEvent(new ui.w.Event('change'));
    assert.equal(ui.$('#output-final').hidden, true, 'a range preview cannot become final');
    assert.match(ui.$('#output-status').textContent, /局部预览/);
    await ui.run('resetOutputs();drawOutputs()');
    assert.ok(ui.$('#output-player').getAttribute('src'), 'reopening the same output restores its playback source');
    await ui.run('project.renders=realRenders;drawOutputs()');
    checks.push('completed render-history fixtures expose owned MP4 playback and export, preserve selection and label partial previews');

    const settingsHTML = await (await realFetch('/settings.html')).text();
    const settings = new JSDOM(settingsHTML, {url: base + '/settings.html', runScripts: 'outside-only'});
    try {
      const settingCalls = [];
      settings.window.fetch = (route, options = {}) => { settingCalls.push((options.method || 'GET') + ' ' + route); return realFetch(route, options); };
      const settingContext = settings.getInternalVMContext();
      const script = await (await realFetch('/settings.js')).text();
      assert.ok(/bootSettings\(\)\.catch\(/.test(script));
      vm.runInContext(script.replace(/bootSettings\(\)\.catch\(/, 'globalThis.settingsBoot = bootSettings(); settingsBoot.catch('), settingContext);
      await vm.runInContext('settingsBoot', settingContext);
      const $ = selector => settings.window.document.querySelector(selector);
      assert.equal($('#settings-app').hidden, false);
      for (const selector of ['#environment-summary', '#generation-status']) assert.equal($(selector).querySelector('input,select,textarea'), null);
      for (const [field, value] of Object.entries({backend: 'indextts', url: 'http://127.0.0.1:8092/v1/audio/speech', model: 'fixture-IndexTTS', voice: 'news_zh', language: 'zh', timeout: '180', key: 'fixture-tts-api-key'})) $('#tts-' + field).value = value;
      $('#tts-form').dispatchEvent(new settings.window.Event('input', {bubbles: true}));
      await $('#tts-form').onsubmit({preventDefault() {}});
      const savedSettings = await (await realFetch('/api/speech/settings')).json();
      assert.equal(savedSettings.backend, 'indextts');
      assert.equal(savedSettings.url, 'http://127.0.0.1:8092/v1/audio/speech');
      assert.equal(savedSettings.model, 'fixture-IndexTTS');
      assert.equal(savedSettings.voice, 'news_zh');
      assert.equal(savedSettings.hasApiKey, true);
      assert.ok(!JSON.stringify(savedSettings).includes('fixture-tts-api-key'));
      assert.equal($('#tts-key').value, '');
      assert.equal(settingCalls.filter(call => call === 'PUT /api/speech/settings').length, 1);
      assert.ok(!settingCalls.some(call => /POST .*\/(jobs|speech|render)(?:$|\?)/.test(call)));

      const settingsFetch = settings.window.fetch;
      let releaseSavedResponse, settingsCommitted;
      const committed = new Promise(resolve => { settingsCommitted = resolve; });
      const responseGate = new Promise(resolve => { releaseSavedResponse = resolve; });
      settings.window.fetch = async (route, options = {}) => {
        const response = await settingsFetch(route, options);
        if (route === '/api/speech/settings' && options.method === 'PUT') {
          settingsCommitted(); await responseGate;
        }
        return response;
      };
      $('#tts-model').value = 'fixture-model-submitted-before-new-input';
      $('#tts-key').value = 'fixture-key-submitted-before-new-input';
      $('#tts-form').dispatchEvent(new settings.window.Event('input', {bubbles: true}));
      const delayedSave = $('#tts-form').onsubmit({preventDefault() {}});
      await committed;
      assert.equal($('#tts-save').disabled, true);
      assert.equal((await (await realFetch('/api/speech/settings')).json()).model, 'fixture-model-submitted-before-new-input');
      $('#tts-model').value = 'new-unsaved-model-during-save';
      $('#tts-key').value = 'new-unsaved-key-during-save';
      $('#tts-form').dispatchEvent(new settings.window.Event('input', {bubbles: true}));
      releaseSavedResponse(); await delayedSave;
      assert.equal($('#tts-model').value, 'new-unsaved-model-during-save');
      assert.equal($('#tts-key').value, 'new-unsaved-key-during-save');
      assert.equal(vm.runInContext('settingsDirty', settingContext), true);
      assert.match($('#tts-settings-state').textContent, /新修改.*待提交/);
      assert.equal((await (await realFetch('/api/speech/settings')).json()).model, 'fixture-model-submitted-before-new-input');
      settings.window.fetch = settingsFetch;
      checks.push('a delayed successful settings save preserves model and key input entered after submission and leaves the new draft unsaved');
    } finally { settings.window.close(); }
    checks.push('the independent settings page persists IndexTTS defaults through HTTP, masks saved keys and keeps connection/generation configuration readonly');

    checks.push(...await backendCompatibility(realFetch, root));
    const report = {passed: true, version: health.version, environment: 'JSDOM + actual Go HTTP API; RunDesk runtime catalog, connection, message receipts and replay use explicit fixtures', checkCount: checks.length, checks, limitations: ['No graphical-browser appearance check.', 'No live Codex/Kun execution or new video rendering in this DOM test.', 'Render-history playback/export selection uses completed metadata fixtures; media integrity is covered by Go tests.']};
    fs.writeFileSync(path.join(root, 'docs/dom-validation-0.28.0.json'), JSON.stringify(report, null, 2) + '\n');
    console.log('Minimal workbench DOM + HTTP passed (' + checks.length + ' checks)');
  } finally {
    ui?.dom.window.close(); proc.kill('SIGTERM');
  }
})().catch(error => { console.error(error); process.exitCode = 1; });
