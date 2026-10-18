/* The browser keeps the original request until the server acknowledges it.
 * Session storage survives reloads in this tab; it contains no script or key. */
(function (root) {
  'use strict';
  function create(getStorage) {
    const key = pid => 'video-speech-pending-v1:' + pid;
    function read(pid) {
      try {
        const raw = getStorage().getItem(key(pid));
        if (!raw) return null;
        const body = JSON.parse(raw);
        if (!body || !/^[a-f0-9]{24}$/.test(body.requestId) || !Number.isInteger(body.revision)) throw new Error();
        return body;
      } catch (_) {
        throw new Error('无法读取待确认配音请求，请允许此站点使用会话存储后重试。');
      }
    }
    function clear(pid, requestId) {
      if (read(pid)?.requestId === requestId) getStorage().removeItem(key(pid));
    }
    function reconcile(pid, jobs) {
      const pending = read(pid);
      if (pending && jobs.some(job => job.requestId === pending.requestId)) clear(pid, pending.requestId);
      return read(pid);
    }
    async function submit(pid, body, send) {
      let pending = read(pid);
      if (!pending) {
        pending = JSON.parse(JSON.stringify(body));
        try { getStorage().setItem(key(pid), JSON.stringify(pending)); }
        catch (_) { throw new Error('无法保存配音请求；尚未提交。请允许此站点使用会话存储。'); }
      }
      try {
        const job = await send(pending);
        if (job?.requestId !== pending.requestId) throw new Error('配音响应缺少匹配的回执');
        clear(pid, pending.requestId);
        return job;
      } catch (error) {
        // These application errors are definite rejections. Authentication,
        // timeouts, network failures and 5xx leave the original request intact.
        if ([400, 404, 409, 422].includes(error.status)) clear(pid, pending.requestId);
        throw error;
      }
    }
    return { read, reconcile, submit };
  }
  if (typeof module === 'object' && module.exports) module.exports = { create };
  else root.SpeechSubmission = { create };
})(typeof globalThis === 'object' ? globalThis : this);
