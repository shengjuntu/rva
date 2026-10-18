'use strict';

const {test} = require('node:test');
const assert = require('node:assert/strict');
const {JSDOM} = require('jsdom');
const fs = require('node:fs');
const path = require('node:path');
const root = path.resolve(__dirname, '..');

const removedControls = [
  'advanced-workspace', 'speech-section', 'speech-start', 'speech-adopt', 'speech-check',
  'render', 'range-render', 'edit-panel', 'edit-json', 'edit-save', 'edit-example',
  'edit-patch', 'patch-apply', 'patch-undo', 'alignment-table', 'production-panel',
  'workflow-backend', 'workflow-save', 'skill-narration', 'skill-editing', 'skill-review',
  'runtime-diagnostics', 'locate-clips', 'locate-results', 'review-timeline', 'review-zoom',
  'review-selection', 'review-select-text', 'hyperframes-project', 'hyperframes-files',
  'tts-settings', 'connect', 'refresh-tools', 'new-session',
];

test('main workbench removes manual production and detailed editing controls from the DOM', () => {
  const dom = new JSDOM(fs.readFileSync(path.join(root, 'web/index.html'), 'utf8'));
  try {
    const document = dom.window.document;
    for (const id of removedControls) assert.equal(document.getElementById(id), null, `${id} must be removed, not hidden`);
    const scripts = [...document.querySelectorAll('script[src]')].map(script => script.getAttribute('src'));
    assert.ok(!scripts.includes('/review.js'), 'multitrack review is not loaded by this release');
    for (const id of ['projects', 'narration-text', 'workflow-instructions', 'upload-folder', 'folder-files', 'upload-button', 'upload', 'draft-start', 'conversation', 'prompt', 'composer']) {
      assert.ok(document.getElementById(id), `missing primary workflow ${id}`);
    }
    assert.equal(document.querySelectorAll('#app input[type=range]').length, 0, 'no editing timeline or position controls');
  } finally { dom.window.close(); }
});

test('configuration has a separate settings page and is absent from the main workbench', () => {
  const main = new JSDOM(fs.readFileSync(path.join(root, 'web/index.html'), 'utf8'), {url: 'http://127.0.0.1:3220'});
  const settings = new JSDOM(fs.readFileSync(path.join(root, 'web/settings.html'), 'utf8'));
  try {
    const link = main.window.document.querySelector('#settings-link');
    assert.ok(link && link.tagName === 'A');
    assert.equal(new URL(link.href).pathname, '/settings.html');
    for (const id of ['tts-url', 'tts-key', 'tts-model', 'tts-voice', 'tts-save', 'environment', 'capability-list']) {
      assert.equal(main.window.document.getElementById(id), null, `configuration ${id} leaked into production`);
    }
    assert.ok(settings.window.document.querySelector('#tts-save'), 'IndexTTS configuration remains independently available');
    for (const id of ['draft-start', 'speech-start', 'render', 'edit-json', 'alignment-table']) {
      assert.equal(settings.window.document.getElementById(id), null, `settings must not recreate manual production ${id}`);
    }
  } finally { main.window.close(); settings.window.close(); }
});

module.exports = {removedControls};
