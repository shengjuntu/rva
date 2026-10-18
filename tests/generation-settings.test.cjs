'use strict';

const {test} = require('node:test');
const assert = require('node:assert/strict');
const {JSDOM} = require('jsdom');
const fs = require('node:fs');
const path = require('node:path');
const vm = require('node:vm');
const root = path.resolve(__dirname, '..');

function setup() {
  const dom = new JSDOM(fs.readFileSync(path.join(root, 'web/settings.html'), 'utf8'), {url: 'http://127.0.0.1:3220', runScripts: 'outside-only'});
  const calls = [];
  dom.window.fetch = async route => {
    calls.push(route);
    const provider = {configured: true, state: 'configured', model: 'minimax_h3_fl2va', reasons: [], verified: route.includes('probe=1') ? 'comfyui_nodes_models' : 'configuration_only'};
    return {ok: true, json: async () => ({capabilities: [{id: 'video_generation', providers: [provider]}]})};
  };
  const ctx = dom.getInternalVMContext();
  const source = fs.readFileSync(path.join(root, 'web/settings.js'), 'utf8').replace(/bootSettings\(\)\.catch\([\s\S]*$/, '');
  vm.runInContext(source, ctx);
  return {dom, calls, run: code => vm.runInContext(code, ctx), text: () => dom.window.document.querySelector('#generation-status').textContent};
}

test('generation settings probes only on explicit refresh and distinguishes a probe from generation', async () => {
  const ui = setup();
  try {
    await ui.run('loadServices()');
    assert.deepEqual(ui.calls, ['/api/capabilities']);
    assert.match(ui.text(), /已配置服务，尚未验证真实生成/);
    await ui.dom.window.document.querySelector('#refresh-services').onclick();
    assert.deepEqual(ui.calls, ['/api/capabilities', '/api/capabilities?probe=1']);
    assert.match(ui.text(), /节点与模型列表检查通过；尚未验证真实生成/);
    assert.ok(!ui.calls.some(route => route.includes('/jobs')));
    assert.equal(ui.dom.window.document.querySelector('#refresh-services').disabled, false);
  } finally { ui.dom.window.close(); }
});

test('late configuration response cannot overwrite the newer ComfyUI probe result', async () => {
  const ui = setup();
  try {
    const originalFetch = ui.dom.window.fetch;
    let release;
    ui.dom.window.fetch = async route => {
      const response = await originalFetch(route);
      if (!route.includes('probe=1')) await new Promise(resolve => { release = resolve; });
      return response;
    };
    const old = ui.run('loadServices()');
    await new Promise(resolve => setImmediate(resolve));
    await ui.run('loadServices(true)');
    assert.match(ui.text(), /模型列表检查通过/);
    release(); await old;
    assert.match(ui.text(), /模型列表检查通过/);
  } finally { ui.dom.window.close(); }
});
