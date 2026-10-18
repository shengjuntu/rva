'use strict';

// Legacy authoring remains an API contract. These checks deliberately do not
// recreate manual production controls in the simplified workbench.
const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');

module.exports = async function backendCompatibility(fetchAPI, root) {
  const checks = [];
  async function request(route, body, method = body === undefined ? 'GET' : 'POST', status = 200) {
    const response = await fetchAPI(route, {
      method,
      ...(body === undefined ? {} : {headers: {'Content-Type': 'application/json'}, body: JSON.stringify(body)}),
    });
    const result = await response.json();
    assert.equal(response.status, status, `${method} ${route}: ${JSON.stringify(result)}`);
    return result;
  }
  let p = await request('/api/projects', {name: 'API兼容回归'}, 'POST', 201);
  const projectPath = '/api/projects/' + p.id;
  assert.equal(p.workflow.editingBackend, 'hyperframes');
  checks.push('new projects default to HyperFrames');
  p = await request(projectPath + '/workflow', {revision: p.revision, workflow: {...p.workflow, editingBackend: 'native'}}, 'PUT');
  p = await request(projectPath + '/production/text', {revision: p.revision, text: '第一条事实。第二条说明！'}, 'PUT');
  assert.equal(p.shots.length, 2);
  const upload = new FormData();
  upload.append('file', new Blob([fs.readFileSync(path.join(root, 'docs/preview.png'))], {type: 'image/png'}), 'API新闻图.png');
  const uploaded = await fetchAPI(projectPath + '/assets', {method: 'POST', body: upload});
  assert.equal(uploaded.status, 201);
  p = await uploaded.json();
  const asset = p.assets[0];
  p.shots[0].assetId = asset.id;
  p = await request(projectPath, p, 'PUT');
  p = await request(projectPath + '/production/text', {revision: p.revision, text: '新增导语。第一条事实。第二条说明！'}, 'PUT');
  assert.equal(p.shots.length, 3);
  assert.equal(p.shots[1].assetId, asset.id);
  assert.equal(p.production.text, '新增导语。第一条事实。第二条说明！');
  const illegal = JSON.parse(JSON.stringify(p));
  illegal.shots[1].text = '未经正文入口替换';
  const rejected = await fetchAPI(projectPath, {method: 'PUT', headers: {'Content-Type': 'application/json'}, body: JSON.stringify(illegal)});
  assert.equal(rejected.status, 409);
  assert.equal((await request(projectPath)).shots[1].text, '第一条事实。');
  checks.push('legacy text projection preserves mapped assets after a new lead and rejects direct canonical-text mutation');

  const preview = await request('/api/mcp', {jsonrpc: '2.0', id: 1, method: 'tools/call', params: {name: 'video_asset_inspect', arguments: {projectId: p.id, assetId: asset.id}}});
  assert.ok(preview.result.content.some(item => item.type === 'image'));
  const tools = await request('/api/mcp', {jsonrpc: '2.0', id: 2, method: 'tools/list'});
  for (const name of ['video_hyperframes_get', 'video_hyperframes_update', 'video_hyperframes_check', 'video_review_timeline_get', 'video_edit_update', 'video_speech_start']) {
    assert.equal(tools.result.tools.filter(tool => tool.name === name).length, 1, name);
  }
  checks.push('actual HTTP MCP returns image content and preserves HyperFrames, native DSL and speech tool registration');

  const imported = {name: 'generated-note.txt', data: Buffer.from('素材导入契约测试').toString('base64'), requestId: 'b'.repeat(24)};
  const first = await request(projectPath + '/assets/import', imported, 'POST', 201);
  const retry = await request(projectPath + '/assets/import', imported);
  assert.equal(first.asset.id, retry.asset.id);
  assert.equal(first.revision, retry.revision);
  await request(projectPath + '/assets/import', {...imported, data: Buffer.from('changed').toString('base64')}, 'POST', 409);
  await request(projectPath + '/jobs', {type: 'image_generation', revision: retry.revision, requestId: 'c'.repeat(24)}, 'POST', 409);
  const jobs = await request(projectPath + '/jobs');
  assert.equal(jobs.jobs.length, 0);
  checks.push('actual HTTP import retries reuse receipts, reused request IDs with changed content conflict and unconfigured generation creates no job');

  p = await request(projectPath);
  p = await request(projectPath + '/narration', {revision: p.revision, text: '独立口播正文。'}, 'PUT');
  const sourceHash = require('node:crypto').createHash('sha256').update(p.narrationText).digest('hex');
  const dsl = {version: 1, textSourceHash: sourceHash, canvas: {width: 320, height: 180, fps: 30}, tracks: [{id: 'visuals', kind: 'video'}, {id: 'titles', kind: 'text'}], clips: [
    {id: 'opening_visual', track: 'visuals', asset: asset.id, start: 0, duration: 3, layout: {fit: 'cover'}},
    {id: 'opening_title', track: 'titles', start: 0, duration: 3, text: 'API标题', textRefs: [{quote: p.narrationText}]},
  ]};
  const saved = await request(projectPath + '/edit', {revision: p.revision, dsl}, 'PUT');
  const edit = await request(projectPath + '/edit');
  assert.equal(edit.active, true);
  assert.equal(edit.ir.clips.length, 2);
  p = await request(projectPath + '/edit/active', {revision: saved.revision, active: false});
  const retained = await request(projectPath + '/edit');
  assert.equal(retained.active, false);
  assert.deepEqual(retained.dsl, edit.dsl);
  const patch = {revision: p.revision, changes: [{id: 'opening_title', op: 'update', fields: {text: 'Changed title'}}]};
  const previewPatch = await request(projectPath + '/edit/patch/preview', patch);
  assert.ok(previewPatch.affectedClipIds.includes('opening_title'));
  assert.equal(previewPatch.saved, false);
  assert.equal((await request(projectPath + '/edit')).dsl.clips[1].text, 'API标题');
  const applied = await request(projectPath + '/edit/patch/apply', patch);
  const undone = await request(projectPath + '/edit/undo', {revision: applied.revision});
  assert.equal((await request(projectPath + '/edit')).dsl.clips[1].text, 'API标题');
  const redone = await request(projectPath + '/edit/redo', {revision: undone.revision});
  assert.equal((await request(projectPath + '/edit')).dsl.clips[1].text, 'Changed title');
  await request(projectPath + '/edit/undo', {revision: redone.revision});
  checks.push('native DSL save, compilation, mode switch, non-mutating patch preview, apply, undo and redo remain available over HTTP');

  p = await request(projectPath);
  await request(projectPath + '/edit/active', {revision: p.revision, active: true});
  const located = await request(projectPath + '/edit/locate', {quote: '独立口播正文。'});
  assert.equal(located.current, true);
  assert.equal(located.matches[0].clipId, 'opening_title');
  assert.equal(located.query.quote, '独立口播正文。');
  const review = await request(projectPath + '/review-timeline');
  assert.equal(review.text, '独立口播正文。');
  assert.equal(review.current, true);
  assert.equal(review.source, 'compiled');
  assert.ok(review.tracks.length >= 4);
  const capabilities = await request('/api/capabilities');
  assert.ok(capabilities);
  const diagnostics = await request(projectPath + '/diagnostics', {});
  assert.equal(diagnostics.modelVisionVerified, false);
  assert.ok(diagnostics.rows.length > 0);
  checks.push('backend exact-text locator and diagnostics retain explicit evidence boundaries without manual UI');
  return checks;
};
