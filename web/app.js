"use strict";
const TOOL_VERSION = "0.19.0";
const $ = (s) => document.querySelector(s),
  el = (tag, text, cls) => {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  };
let ttsSettings = null, savingTTS = false, discoveringVoices = false;
const speechSubmission = SpeechSubmission.create(() => sessionStorage);
const renderSubmission = SpeechSubmission.create(() => sessionStorage,"video-render-pending-v1","渲染");
let startingRender=false;
let productionDirty = false;
let editDSLDirty=false,editDSLBase=0,editPanelLoading=false;
let serviceJobsKey="", serviceJobsLoading=false;
let captionDraft = null, savePromise = null, speechKey = "", startingSpeech = false;
let project = null,
  selected = 0,
  dirty = false,
  editSerial = 0,
  epoch = 0,
  after = 0,
  session = null,
  polling = false,
  sending = false,
  renderKey = "",
  approvalKey = "",
  rows = new Map();
const api = async (path, body, method, timeoutMs = 0) => {
  const opts = { method: method || (body === undefined ? "GET" : "POST") };
  if (body !== undefined) {
    opts.body = body instanceof FormData ? body : JSON.stringify(body);
    if (!(body instanceof FormData))
      opts.headers = { "Content-Type": "application/json" };
  }
  const controller = timeoutMs ? new AbortController() : null;
  if (controller) opts.signal = controller.signal;
  const timer = controller ? setTimeout(() => controller.abort(), timeoutMs) : null;
  try {
  const r = await fetch(path, opts);
  const data = await r.json();
  if (!r.ok) {
    const e = new Error(data.error || data.diagnostics?.map(d=>d.target+": "+d.message).join("\n") || r.statusText);
    e.status = r.status;
    e.code = data.code;
    e.uncertain = data.uncertain;
    e.requestId = data.requestId;
    throw e;
  }
  return data;
  } finally { if (timer !== null) clearTimeout(timer); }
};
const path = (tail = "") => "/api/projects/" + project.id + tail;
function toast(msg) {
  $("#toast").textContent = msg;
  $("#toast").hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => ($("#toast").hidden = true), 5000);
}
function action(fn) {
  return async (e) => {
    try {
      await fn(e);
    } catch (err) {
      toast(err.message);
    }
  };
}
function changed() {
  dirty = true;
  editSerial++;
  $("#save-state").textContent = "有未保存的编辑";
  drawSubtitleState();
}
function id() {
  return [...crypto.getRandomValues(new Uint8Array(12))]
    .map((x) => x.toString(16).padStart(2, "0"))
    .join("");
}
// Save before sending. A refresh or unknown response must reuse the same input.
function pendingMessage(pid) {
  try { return JSON.parse(sessionStorage.getItem("video-message:" + pid) || "null"); }
  catch { return null; }
}
function rememberMessage(pid, body) {
  if (body) sessionStorage.setItem("video-message:" + pid, JSON.stringify(body));
  else sessionStorage.removeItem("video-message:" + pid);
}
function reconcileMessage(submission) {
  if (!project) return;
  let pending = pendingMessage(project.id);
  if (!pending && submission?.state === "pending") {
    pending = {text: submission.text, requestId: submission.id};
    if (submission.turnId) pending.expectedTurnId = submission.turnId;
    rememberMessage(project.id, pending);
  }
  if (pending && submission?.id === pending.requestId && ["accepted", "failed", "canceled"].includes(submission.state)) {
    rememberMessage(project.id, null);
    if (submission.state === "accepted" && $("#prompt").value.trim() === pending.text) $("#prompt").value = "";
    $("#send-state").textContent = submission.state === "accepted" ? "指令已接收" : submission.error || "原提交已结束";
    pending = null;
  }
  if (pending) {
    if (!$("#prompt").value) $("#prompt").value = pending.text;
    $("#send-state").textContent = submission?.cancelRequested ? "正在确认停止；不会重新启动任务" : "原指令待确认；重试会沿用原请求编号和内容";
    $("#send").textContent = "确认原提交";
  }
  return pending;
}
function showApp() {
  $("#login").hidden = true;
  $("#app").hidden = false;
}
async function listProjects() {
  const list = await api("/api/projects");
  const select = $("#projects");
  select.replaceChildren();
  if (!list.length) select.append(el("option", "尚无项目"));
  for (const p of list) {
    const o = el("option", p.name);
    o.value = p.id;
    select.append(o);
  }
  if (project) select.value = project.id;
  return list;
}
async function openProject(pid) {
  const p = await api("/api/projects/" + pid);
  epoch++;
  for (const k of ["narration", "music"]) $("#" + k + "-preview").pause();
  project = p;
  captionDraft = null;
  productionDirty = false;
  editDSLDirty=false;editDSLBase=p.revision;
  selected = 0;
  dirty = false;
  after = 0;
  session = null;
  rows = new Map();
  renderKey = "";
  serviceJobsKey="";
  speechKey = "";
  approvalKey = "";
  $("#conversation").replaceChildren(
    el(
      "p",
      p.sessionId ? "载入对话…" : "连接 RunDesk，让 Agent 协助规划和修改分镜。",
      "chat-empty",
    ),
  );
  $("#approvals").replaceChildren();
  $("#conflict").hidden = true;
  $("#prompt").value = "";
  $("#send-state").textContent = "";
  $("#player").pause();
  $("#player").hidden = true;
  $("#poster").hidden = false;
  drawProject();
  reconcileMessage(p.agentSubmission);
}
let narrationDirty=false,narrationBase=0,narrationProject="";
function drawProject() {
 drawWorkflow();
 if(project && narrationProject!==project.id){narrationDirty=false;narrationProject=project.id;}
 if(project && !narrationDirty) narrationBase=project.revision;
 if(project && !narrationDirty) $("#narration-text").value=project.narrationText||"";
  if (!project) return;
  $("#project-name").textContent = project.name;
  $("#brief").value = project.brief;
  $("#save-state").textContent = dirty
    ? "有未保存的编辑"
    : "已保存 · 修订 " + project.revision;
  $("#connect").disabled = !!project.sessionId;
  $("#refresh-tools").hidden = !project.sessionId;
  $("#new-session").hidden = !project.sessionId;
  $("#refresh-tools").textContent =
    project.toolsVersion === TOOL_VERSION
      ? "重新配置工具"
      : "升级项目工具至 v" + TOOL_VERSION;
  $("#connect").textContent = project.sessionId
    ? "已配置实例 Skill 与 MCP"
    : "连接 RunDesk 并配置工具";
  $("#send").disabled = !project.sessionId || sending;
  $("#agent-status").textContent = project.sessionId ? "待命" : "未连接";
  drawAssets();
  drawShots(true);
  drawAudio();
  drawRenders();
  drawNews();
  drawSubtitles();
  drawSpeech();
  drawProduction();
  drawEditingMode();
  if(!editDSLDirty)loadEditPanel().catch(()=>{});
  loadProductionJobs().catch(()=>{});
}
function assetURL(a) {
  return path("/assets/" + a.id);
}
function drawAssets() {
  const box = $("#assets");
  box.replaceChildren();
  if (!project.assets.length)
    box.append(
      el("p", "还没有素材，纯文字分镜也可以生成视频。", "muted small"),
    );
  for (const a of project.assets) {
    const row = el("div", undefined, "asset");
    if (a.mime.startsWith("image/")) {
      const img = el("img");
      img.src = assetURL(a);
      img.alt = a.name;
      img.loading = "lazy";
      row.append(img);
    }
    const d = el("div");
    d.append(el("strong", a.name));
    if (a.source) d.append(el("small", "新闻配图候选 · 使用前确认版权与内容", "muted"));
    const link = el(
      "a",
      "查看 · " + (a.size / 1024 / 1024).toFixed(1) + " MiB",
    );
    link.href = assetURL(a);
    link.target = "_blank";
    link.rel = "noopener";
    d.append(link);
    row.append(d);
    box.append(row);
  }
  const sel = $("#shot-asset");
  sel.replaceChildren();
  const empty = el("option", "纯文字画面");
  empty.value = "";
  sel.append(empty);
  for (const a of project.assets.filter((x) =>
    /^(image|video)\//.test(x.mime),
  )) {
    const o = el("option", a.name);
    o.value = a.id;
    sel.append(o);
  }
}
function drawShots(editor = false) {
  const box = $("#shots");
  box.replaceChildren();
  selected = Math.max(0, Math.min(selected, project.shots.length - 1));
  project.shots.forEach((s, i) => {
    const b = el(
      "button",
      undefined,
      "shot" + (i === selected ? " active" : ""),
    );
    b.append(
      el("div", String(i + 1).padStart(2, "0"), "number"),
      el("strong", s.title || "未命名镜头"),
      el("span", s.duration + " 秒"),
    );
    b.onclick = () => {
      selected = i;
      drawShots(true);
      showPoster();
    };
    box.append(b);
  });
  $("#shot-count").textContent = project.shots.length;
  $("#duration").textContent =
    project.shots.reduce((n, s) => n + Number(s.duration || 0), 0) + " 秒";
  const s = project.shots[selected];
  $("#editor").hidden = !s;
  for (const name of ["shot-up", "shot-down", "delete-shot"])
    $("#" + name).disabled = !s || !!project.production;
  $("#add-shot").disabled = !!project.production;
  $("#shot-text").readOnly = !!project.production;
  drawAudioTimeline();
  if (s && editor) {
    $("#shot-title").value = s.title;
    $("#shot-text").value = s.text;
    $("#shot-duration").value = s.duration;
    $("#shot-asset").value = s.assetId;
  }
  if (s) {
    $("#shot-number").textContent =
      String(selected + 1).padStart(2, "0") +
      " / " +
      String(project.shots.length).padStart(2, "0");
    $("#poster-title").textContent = s.title;
    $("#poster-text").textContent = s.text;
    const a = project.assets.find((x) => x.id === s.assetId);
    $("#poster-image").hidden = !a?.mime.startsWith("image/");
    $("#poster").classList.toggle("text-only", !s.assetId);
    if (a?.mime.startsWith("image/")) $("#poster-image").src = assetURL(a);
  } else {
    $("#poster-title").textContent = "开始你的第一条视频";
    $("#poster-text").textContent = "添加镜头，或让创作助手规划分镜。";
    $("#poster-image").hidden = true;
  }
}
function showPoster() {
  $("#player").pause();
  $("#player").hidden = true;
  $("#poster").hidden = false;
  $("#preview-label").textContent = "镜头预览";
}
function save() {
  if (savePromise) return savePromise;
  savePromise = saveChanges().finally(() => { savePromise = null; });
  return savePromise;
}
async function saveChanges() {
  if (!project) return;
  if (!dirty) return project;
  const pid = project.id, gen = epoch, serial = editSerial;
  const captions = captionDraft ? {...captionDraft} : null;
  const textDraft = productionDirty ? $("#production-text").value : null;
  try {
    let p = await api('/api/projects/' + pid, project, "PUT");
    if (gen === epoch) project.revision = p.revision;
    if (textDraft !== null) {
      p = await api('/api/projects/' + pid + '/production/text', {revision:p.revision,text:textDraft}, 'PUT');
      if (gen === epoch) project.revision = p.revision;
    }
    if (captions) {
      p = await api('/api/projects/' + pid + '/subtitles', {...captions, revision:p.revision, reviewed:false}, 'PUT');
    }
    if (gen !== epoch) return p;
    if (serial === editSerial) {
      project = p;
      captionDraft = null;
      productionDirty = false;
      dirty = false;
      drawProject();
    } else {
      project.revision = p.revision;
      if (textDraft !== null) {
        const local = new Map(project.shots.map(s=>[s.id,s]));
        project.production = p.production;
        project.shots = p.shots.map(s=>{const edit=local.get(s.id);return edit && edit.text===s.text ? {...s,title:edit.title,assetId:edit.assetId,duration:edit.duration,visualReason:edit.visualReason} : s});
      }
      project.assets = p.assets;
      project.renders = p.renders;
      project.speech = p.speech;
      project.subtitles = p.subtitles;
      drawSubtitleState();
    }
    $("#conflict").hidden = true;
    return p;
  } catch (e) {
    if (gen === epoch && e.status === 409) $("#conflict").hidden = false;
    throw e;
  }
}
const states = {
  running: "正在生成",
  stopping: "正在取消",
  completed: "已完成",
  failed: "失败",
  interrupted: "已中断",
};
function drawRenders() {
  try {renderSubmission.reconcile(project.id,project.renders)}catch(e){toast(e.message)}
  const key = JSON.stringify(project.renders);
  if (key === renderKey) return;
  renderKey = key;
  const box = $("#renders");
  box.replaceChildren();
  if (!project.renders.length)
    box.append(el("p", "保存分镜与音轨后生成第一版视频。", "muted small"));
  for (const r of [...project.renders].reverse()) {
    const row = el("div", undefined, "render-row"),
      info = el("div"),
      links = el("div");
    info.append(
      el("strong", (project.final?.renderId===r.id ? "终稿记录 · " : project.production?.finalRenderId === r.id ? "终稿 · " : r.range ? "局部预览 · " : r.kind === "draft" ? "初稿预览 · " : "成片 · ") + "修订 " + r.revision + " · " + (states[r.state] || r.state)),
      el(
        "small",
        new Date(r.created).toLocaleString() +
          " · " +
          r.duration +
          " 秒" +
          (r.hasAudio ? " · 含音轨" : " · 无声"),
      ),
    );
    info.append(el("small","ID："+r.id+(r.reusedFrom?" · 复用已校验渲染："+r.reusedFrom:"")));
 if (r.error) info.append(el("div", r.error, "render-error"));
    const base = path("/renders/" + r.id + "/");
    if (r.state === "completed") {
      const play = el("button", "播放");
      play.onclick = () => {
        $("#poster").hidden = true;
        $("#player").hidden = false;
        $("#player").src = base + "final.mp4";
        $("#preview-label").textContent = "成片 · 修订 " + r.revision;
        $("#player")
          .play()
          .catch(() => {});
      };
      links.append(play);
      const dl = el("a", "下载 MP4");
      dl.href = base + "final.mp4?download";
      links.append(dl);
      if (r.hasSubtitles) {
        const sub = el('a','字幕 SRT'); sub.href = base + 'subtitles.srt?download'; links.append(sub);
      }
    }
    if (r.state === "running") {
      const stop = el("button", "取消");
      stop.onclick = action(async () => {
        await api(base + "stop", {});
        await poll();
      });
      links.append(stop);
    }
    for (const [file, label] of [
      ["render.log", "日志"],
      ["snapshot.json", "快照"],
    ]) {
      const l = el("a", label);
      l.href = base + file;
      l.target = "_blank";
      l.rel = "noopener";
      links.append(l);
    }
    row.append(info, links);
    box.append(row);
  }
}
function eventRow(key, type) {
  let row = rows.get(key);
  if (row) return row;
  const box = $("#conversation");
  if (!rows.size) box.replaceChildren();
  const node =
    type === "user" || type === "agentMessage"
      ? el(
          "div",
          undefined,
          "message " + (type === "user" ? "user" : "assistant"),
        )
      : el("details", undefined, "event");
  row = { node, item: { type }, text: "", summary: null, body: null };
  if (node.tagName === "DETAILS") {
    row.summary = el("summary");
    row.body = el("pre");
    node.append(row.summary, row.body);
  }
  rows.set(key, row);
  box.append(node);
  return row;
}
function drawEvent(row) {
  const i = row.item;
  if (!row.summary) {
    row.node.textContent = i.text || "";
    return;
  }
  const names = {
    reasoning: "推理摘要",
    commandExecution: "命令执行",
    mcpToolCall: "项目工具",
    contextCompaction: "上下文整理",
    fileChange: "文件变更",
  };
  row.summary.textContent =
    (names[i.type] || i.type) +
    (i.tool ? " · " + i.tool : "") +
    (i.status ? " · " + i.status : "");
  row.body.textContent =
    i.type === "reasoning"
      ? (i.summary || [])
          .map((x) => (typeof x === "string" ? x : x.text || ""))
          .join("\n")
      : JSON.stringify(i, null, 2);
}
function consume(events) {
  const box = $("#conversation"),
    bottom = box.scrollHeight - box.scrollTop - box.clientHeight < 90;
  for (const ev of events) {
    after = Math.max(after, ev.id);
    const d = ev.data || {},
      p = d.params || {};
    if (ev.method === "run/input" || ev.method === "run/steer") {
      const row = eventRow("input:" + ev.id, "user");
      row.item.text = (d.input?.text || "").replace(
        /^\[Video App[^]*?\n用户要求：\n/,
        "",
      );
      if (ev.method === "run/steer") row.item.text = "补充：" + row.item.text;
      drawEvent(row);
    } else if (
      ev.direction === "in" &&
      (ev.method === "item/started" || ev.method === "item/completed")
    ) {
      const i = p.item;
      if (!i || i.type === "userMessage") continue;
      const row = eventRow((p.turnId || "") + ":" + i.id, i.type);
      Object.assign(row.item, i);
      drawEvent(row);
    } else if (
      (ev.direction === "in" && ev.method.endsWith("Delta")) ||
      (ev.direction === "in" && ev.method === "item/agentMessage/delta")
    ) {
      const type = ev.method.includes("agentMessage")
        ? "agentMessage"
        : ev.method.includes("reasoning")
          ? "reasoning"
          : ev.method.includes("commandExecution")
            ? "commandExecution"
            : null;
      if (!type) continue;
      const row = eventRow((p.turnId || "") + ":" + p.itemId, type);
      if (type === "agentMessage")
        row.item.text = (row.item.text || "") + (p.delta || "");
      else if (type === "commandExecution")
        row.item.aggregatedOutput =
          (row.item.aggregatedOutput || "") + (p.delta || "");
      else {
        row.item.summary ||= [];
        const n =
          Number.isInteger(p.summaryIndex) && p.summaryIndex < 1000
            ? p.summaryIndex
            : 0;
        row.item.summary[n] = (row.item.summary[n] || "") + (p.delta || "");
      }
      drawEvent(row);
    } else if (
      ev.direction === "in" &&
      ["error", "warning"].includes(ev.method)
    ) {
      const row = eventRow("event:" + ev.id, ev.method);
      row.item = p;
      row.item.type = ev.method;
      drawEvent(row);
    }
  }
  if (bottom) box.scrollTop = box.scrollHeight;
}
function drawApprovals(list) {
  const pending = list.filter((a) => a.status === "pending");
  const key = JSON.stringify(pending);
  if (key === approvalKey) return;
  approvalKey = key;
  const box = $("#approvals");
  box.replaceChildren();
  for (const a of pending) {
    const row = el("div", undefined, "approval"),
      p = a.request?.params || {};
    row.append(
      el("strong", "等待授权"),
      el("p", p.reason || p.command || a.request?.method),
    );
    const details = el("details");
    details.append(
      el("summary", "查看请求"),
      el("pre", JSON.stringify(p, null, 2)),
    );
    row.append(details);
    const decisions = a.decisions || p.availableDecisions || [];
    if (!decisions.length)
      row.append(el("p", "此类交互请在 RunDesk 页面处理。"));
    for (const d of decisions) {
      const labels = {
        accept: "允许本次",
        acceptForSession: "本会话允许",
        decline: "拒绝",
        cancel: "取消",
      };
      const label =
        typeof d === "string" ? labels[d] || d : "允许并保存指定规则";
      const b = el("button", label);
      b.onclick = action(async () => {
        b.disabled = true;
        try {
          await api(path("/approvals/" + a.id), { decision: d });
          approvalKey = "";
        } finally {
          b.disabled = false;
        }
      });
      row.append(b);
    }
    box.append(row);
  }
}
async function poll() {
  if (polling || !project || $("#app").hidden) return;
  polling = true;
  const gen = epoch,
    pid = project.id;
  try {
    const p = await api("/api/projects/" + pid);
    if (gen !== epoch) return;
    if (p.revision !== project.revision) {
      if (dirty) {
        $("#conflict").hidden = false;
        project.renders = p.renders;
      } else {
        project = p;
        drawProject();
      }
    } else {
      project.renders = p.renders;
      project.sessionId = p.sessionId;
      project.workspaceId = p.workspaceId;
    }
    project.speech = p.speech;
    drawSpeech();
    drawRenders();
    loadProductionJobs().catch(()=>{});
    if (project.sessionId) {
      const [s, events, approvals] = await Promise.all([
        api(path("/agent")),
        api(path("/events?after=" + after)),
        api(path("/approvals")),
      ]);
      if (gen !== epoch) return;
      session = s;
      project.agentSubmission = s.appSubmission;
      project.agentRunId = s.appRunId;
      $("#agent-status").textContent =
        {
          running: "正在创作",
          waiting: "等待授权",
          starting: "启动中",
          idle: "待命",
          error: "出错",
          stopped: "已停止",
          completed: "已完成",
          interrupted: "已中断",
          stopping: "停止中",
        }[s.status] || s.status;
      $("#agent-stop").hidden = !["running", "waiting", "starting"].includes(
        s.status,
      );
      $("#send").textContent =
        s.turnId && ["running", "waiting"].includes(s.status)
          ? "补充指令"
          : "发送";
      const pending = reconcileMessage(s.appSubmission);
      if (pending) $("#agent-stop").hidden = false;
      consume(events);
      drawApprovals(approvals);
    }
  } catch (e) {
    if (gen === epoch) $("#send-state").textContent = e.message;
  } finally {
    polling = false;
  }
}
$("#login-form").onsubmit = async (e) => {
  e.preventDefault();
  try {
    await api("/login", { token: $("#token").value });
    $("#token").value = "";
    await boot();
  } catch (err) {
    $("#login-error").textContent = err.message;
  }
};
$("#logout").onclick = action(async () => {
  if (dirty && !confirm("有未保存的编辑，仍要退出？")) return;
  await api("/logout", {});
  location.reload();
});
$("#new-project").onclick = () => $("#new-dialog").showModal();
$("#cancel-new").onclick = () => $("#new-dialog").close();
$("#new-form").onsubmit = action(async (e) => {
  e.preventDefault();
  if (dirty) await save();
  const p = await api("/api/projects", { name: $("#new-name").value });
  $("#new-dialog").close();
  await openProject(p.id);
  await listProjects();
});
$("#projects").onchange = action(async (e) => {
  const pid = e.target.value;
  if ((dirty||editDSLDirty) && !confirm("放弃当前未保存编辑并切换项目？")) {
    $("#projects").value = project.id;
    return;
  }
  await openProject(pid);
});
$("#save").onclick = action(async()=>{if(editDSLDirty)await saveEditDSL();else await save()});
$("#reload-project").onclick = action(async () => {
  if (confirm("重新载入会替换当前草稿，确定继续？"))
    await openProject(project.id);
});
$("#brief").oninput = () => {
  if (project) {
    project.brief = $("#brief").value;
    changed();
  }
};
for (const [sel, key] of [
  ["#shot-title", "title"],
  ["#shot-text", "text"],
  ["#shot-duration", "duration"],
  ["#shot-asset", "assetId"],
])
  $(sel).oninput = () => {
    const s = project?.shots[selected];
    if (!s) return;
    s[key] = key === "duration" ? Number($(sel).value) : $(sel).value;
    changed();
    drawShots();
    showPoster();
  };
$("#add-shot").onclick = () => {
  if (!project) {
    $("#new-dialog").showModal();
    return;
  }
  project.shots.push({
    id: id(),
    title: "新的镜头",
    text: "",
    duration: 5,
    assetId: "",
  });
  selected = project.shots.length - 1;
  changed();
  drawShots(true);
  showPoster();
};
$("#delete-shot").onclick = () => {
  if (project?.shots.length) {
    project.shots.splice(selected, 1);
    changed();
    drawShots(true);
  }
};
function move(offset) {
  const shots = project?.shots;
  if (!shots) return;
  const n = selected + offset;
  if (n < 0 || n >= shots.length) return;
  [shots[n], shots[selected]] = [shots[selected], shots[n]];
  selected = n;
  changed();
  drawShots(true);
}
$("#shot-up").onclick = () => move(-1);
$("#shot-down").onclick = () => move(1);
$("#show-storyboard").onclick = () => { $("#visual-details").open=true;showPoster(); };
$("#upload-button").onclick = () => {
  if (project) $("#upload").click();
  else $("#new-dialog").showModal();
};
$("#upload").onchange = action(async () => {
  await save();
  const pid = project.id,
    gen = epoch;
  $("#upload-button").disabled = true;
  try {
    for (const file of $("#upload").files) {
      const body = new FormData();
      body.append("file", file);
      const p = await api("/api/projects/" + pid + "/assets", body);
      if (gen === epoch) {
        if (dirty) {
          project.assets = p.assets;
          $("#conflict").hidden = false;
          drawAssets();
          drawShots();
          drawAudio();
        } else {
          project = p;
          drawProject();
        }
      }
    }
    toast("素材已上传");
  } finally {
    $("#upload-button").disabled = false;
    $("#upload").value = "";
  }
});
$("#render").onclick = action(async () => {
  if (!project || startingRender) return;
  const pid=project.id,gen=epoch;startingRender=true;$('#render').disabled=true;
  try {
    const pending=renderSubmission.read(pid);
    if(!pending) {await save();if(editDSLDirty)await saveEditDSL()}
    if(gen!==epoch)return;
    const body=pending||{type:"render",revision:project.revision,requestId:id(),options:{kind:"draft"}};
    const job=await renderSubmission.submit(pid,body,original=>api('/api/projects/'+pid+'/jobs',original));
    if(gen!==epoch)return;
    toast(job.state==='running'?'渲染任务已创建，可以继续编辑':'已确认原渲染任务状态：'+job.state);
    await poll();
  }finally{startingRender=false;$('#render').disabled=false}
});
$("#connect").onclick = action(async () => {
  if (!project) {
    $("#new-dialog").showModal();
    return;
  }
  await save();
  $("#connect").disabled = true;
  const pid = project.id;
  try {
    const p = await api(path("/connect"), {});
    if (project.id === pid) {
      project.sessionId = p.sessionId;
      project.workspaceId = p.workspaceId;
      project.toolsVersion = p.toolsVersion;
      drawProject();
    }
    toast("已配置专用实例、实例 Skill 和 MCP");
  } finally {
    $("#connect").disabled = !!project?.sessionId;
  }
  await poll();
});
async function submitMessage(e) {
  e.preventDefault();
  if (sending || !project?.sessionId) return;
  const text = $("#prompt").value.trim();
  if (!text) return;
  const pid = project.id, gen = epoch;
  let body = pendingMessage(pid);
  if (body && text !== body.text) {
    toast("上一条消息仍待确认；请先确认原提交，再发送新内容"); return;
  }
  await save();
  if (gen !== epoch) return;
  if (!body) {
    body = {text, requestId: id(),skills:(project.workflow?.skills||["video-narration","video-editing","video-review"])};
    if (session?.turnId && ["running", "waiting"].includes(session.status)) body.expectedTurnId = session.turnId;
    rememberMessage(pid, body);
  }
  sending = true;
  $("#send").disabled = true;
  try {
    await api("/api/projects/" + pid + "/messages", body);
    rememberMessage(pid, null);
    if (gen === epoch) {
      if ($("#prompt").value.trim() === body.text) $("#prompt").value = "";
      $("#send-state").textContent = "指令已接收";
    }
  } catch (err) {
    if (err.status >= 400 && err.status < 500 && !err.uncertain) rememberMessage(pid, null);
    if (gen === epoch) $("#send-state").textContent = pendingMessage(pid) ? "接收情况待确认，已保留原指令，可刷新后继续确认" : err.message;
    throw err;
  } finally {
    sending = false;
    $("#send").disabled = !project?.sessionId;
  }
  await poll();
}
$("#composer").onsubmit = action(submitMessage);
$("#prompt").onkeydown = (e) => {
  if (e.key === "Enter" && !e.shiftKey && !e.isComposing) {
    e.preventDefault();
    $("#composer").requestSubmit();
  }
};
$("#agent-stop").onclick = action(async () => {
  const pending = project.agentSubmission;
  const expectedRunId = pending?.state === "pending" && !pending.turnId ? "" : (project.agentRunId || session?.runId || "");
  await api(path("/agent/stop"), {expectedRunId});
  $("#send-state").textContent = "已请求停止 Agent；渲染任务需单独取消";
  await poll();
});
$("#environment").onclick = action(async () => {
  $("#env-dialog").showModal();
  $("#env-info").textContent = "检查中…";
  const env = await api("/api/environment");
  $("#open-rundesk").href = env.rundeskURL + (project?.sessionId ? "/?session="+encodeURIComponent(project.sessionId) : "/#application/rundesk-video-app");
  const box = $("#env-info");
  box.replaceChildren();
  box.append(
    el(
      "div",
      (env.rundeskReady ? "✓" : "×") +
        " RunDesk " +
        (env.rundeskError || "连接正常"),
      "check",
    ),
  );
  if (project?.sessionId) {
    try {
      const config = await api(path("/agent/configuration"));
      box.append(el("div", "模型 · " + (config.session?.model || config.instance?.defaultModel || "运行环境默认"), "check"));
      for (const skill of config.skills || []) box.append(el("div", "Skill · " + skill.name + " · " + ({instance:"实例",project:"项目",other:"其他"}[skill.sourceScope] || skill.sourceScope) + (skill.enabled ? " · 已启用" : " · 未启用"), "check"));
      for (const server of config.mcp?.servers || []) box.append(el("div", "MCP · " + server.name + (server.enabled ? " · 已启用" : " · 未启用"), "check"));
      for (const error of Object.values(config.errors || {})) box.append(el("div", "配置检查 · " + (typeof error === "string" ? error : JSON.stringify(error)), "check"));
    } catch (err) { box.append(el("div", "配置检查 · " + err.message, "check")); }
  }
  if (env.instanceId) {
    const line = el("p", "读取执行环境…", "check"); box.append(line);
    try {
      const runtime = await api("/api/runtime");
      line.textContent = "Agent 环境 · " + runtime.executionMode + " · " + (runtime.environment?.state || "尚未启动") + (runtime.image ? " · " + runtime.image : "");
      if (runtime.executionMode === "docker") {
        const prepare = el("button", "启动并检查 Codex 容器"); box.append(prepare);
        prepare.onclick = action(async () => {
          prepare.disabled = true; line.textContent = "正在启动并检查认证、MCP…";
          try { await api("/api/runtime/prepare", {}); line.textContent = "✓ 容器运行、模型认证与视频 MCP 工具发现通过；尚未调用模型。"; }
          catch (err) { line.textContent = err.message; }
          finally { prepare.disabled = false; }
        });
      }
    } catch (err) { line.textContent = err.message; }
  }
  for (const [name, c] of Object.entries(env.checks)) {
    box.append(
      el(
        "div",
        (c.ready ? "✓ " : "× ") + name + " · " + (c.path || "尚未配置"),
        "check",
      ),
    );
  }
});
$("#close-env").onclick = () => $("#env-dialog").close();
for (const [button, side, cls] of [
  ["#toggle-assets", "#library", "no-assets"],
  ["#toggle-chat", "#chat", "no-chat"],
])
  $(button).onclick = () => {
    $(side).hidden = !$(side).hidden;
    $(".layout").classList.toggle(cls, $(side).hidden);
  };
window.addEventListener("beforeunload", (e) => {
  if (dirty||editDSLDirty) {
    e.preventDefault();
    e.returnValue = "";
  }
});
async function boot() {
  const list = await listProjects();
  showApp();
  await loadTTSSettings();
  loadCapabilities(false).catch(()=>{});
  api("/api/environment")
    .then((e) => {
      $("#open-rundesk").href = e.rundeskURL + (project?.sessionId ? "/?session="+encodeURIComponent(project.sessionId) : "/#application/rundesk-video-app");
    })
    .catch(() => {});
  if (list.length) await openProject(list[0].id);
  else $("#new-dialog").showModal();
}
boot().catch((e) => {
  if (e.status !== 401) $("#login-error").textContent = e.message;
});
setInterval(poll, 1400);

function ensureAudio() {
  if (!project) return null;
  project.audio ||= {};
  project.audio.narration ||= {
    assetId: "",
    volume: 1,
    start: 0,
    sourceStart: 0,
    loop: false,
  };
  project.audio.music ||= {
    assetId: "",
    volume: 0.2,
    start: 0,
    sourceStart: 0,
    loop: true,
  };
  return project.audio;
}
function drawAudio() {
  const mix = ensureAudio();
  if (!mix) return;
  for (const kind of ["narration", "music"]) {
    const t = mix[kind],
      select = $("#" + kind + "-asset");
    select.replaceChildren();
    const empty = el("option", "不使用");
    empty.value = "";
    select.append(empty);
    for (const a of project.assets.filter((a) => a.mime.startsWith("audio/"))) {
      const o = el("option", a.name);
      o.value = a.id;
      select.append(o);
    }
    select.value = t.assetId;
    for (const key of ["start", "sourceStart", "volume"])
      $("#" + kind + "-" + key).value =
        key === "volume" ? Math.round(t[key] * 100) : t[key];
    if (kind === "music") $("#music-loop").checked = t.loop;
    updateAudioPreview(kind);
  }
  drawAudioTimeline();
}
function updateAudioPreview(kind) {
  const t = ensureAudio()[kind],
    a = project.assets.find((a) => a.id === t.assetId),
    player = $("#" + kind + "-preview");
  player.hidden = !a;
  const url = a ? assetURL(a) : "";
  if (player.dataset.source !== url) {
    player.pause();
    player.dataset.source = url;
    if (url) player.src = url;
    else {
      player.removeAttribute("src");
      player.load();
    }
  }
  $("#" + kind + "-info").textContent = a
    ? "素材试听 · " + a.name
    : "未选择音频";
}
function drawAudioTimeline() {
  if (!project) return;
  const svg = $("#audio-timeline");
  svg.replaceChildren();
  const total = project.shots.reduce((n, s) => n + Number(s.duration || 0), 0);
  const node = (tag, attrs, text) => {
    const n = document.createElementNS("http://www.w3.org/2000/svg", tag);
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, String(v));
    if (text) n.textContent = text;
    svg.append(n);
    return n;
  };
  const left = 86,
    width = 530;
  for (const [i, kind] of ["narration", "music"].entries()) {
    const y = 38 + i * 37;
    node(
      "text",
      { x: 0, y: y + 16, fill: "#536d63", "font-size": 13 },
      kind === "music" ? "背景音乐" : "配音",
    );
    node("rect", { x: left, y, width, height: 25, rx: 5, fill: "#e7eeeb" });
    const t = ensureAudio()[kind],
      player = $("#" + kind + "-preview");
    if (t.assetId && total > 0) {
      const source = Number.isFinite(player.duration)
        ? Math.max(0, player.duration - t.sourceStart)
        : null;
      const length =
        t.loop || source === null
          ? Math.max(0, total - t.start)
          : Math.min(source, Math.max(0, total - t.start));
      const start = Math.min(total, Math.max(0, t.start));
      node("rect", {
        x: left + (width * start) / total,
        y,
        width: (width * length) / total,
        height: 25,
        rx: 5,
        fill: kind === "music" ? "#86aab3" : "#438575",
      });
      const title = node(
        "title",
        {},
        (kind === "music" ? "背景音乐" : "配音") +
          "：视频第 " +
          t.start +
          " 秒，音量 " +
          Math.round(t.volume * 100) +
          "%",
      );
      svg.lastElementChild.previousElementSibling?.append(title);
      if (source === 0)
        $("#" + kind + "-info").textContent = "截取起点已超出音频时长，请调整";
    }
  }
  for (const [x, t] of [
    [left, 0],
    [left + width / 2, total / 2],
    [left + width, total],
  ])
    node(
      "text",
      {
        x,
        y: 22,
        fill: "#76867f",
        "font-size": 12,
        "text-anchor":
          x === left ? "start" : x === left + width ? "end" : "middle",
      },
      t.toFixed(1) + " 秒",
    );
}
for (const kind of ["narration", "music"]) {
  $("#" + kind + "-asset").onchange = () => {
    if (!project) return;
    ensureAudio()[kind].assetId = $("#" + kind + "-asset").value;
    changed();
    updateAudioPreview(kind);
    drawAudioTimeline();
  };
  for (const key of ["start", "sourceStart", "volume"])
    $("#" + kind + "-" + key).oninput = () => {
      if (!project) return;
      ensureAudio()[kind][key] =
        Number($("#" + kind + "-" + key).value) / (key === "volume" ? 100 : 1);
      changed();
      drawAudioTimeline();
    };
  $("#" + kind + "-preview").onloadedmetadata = () => {
    if (!project) return;
    const p = $("#" + kind + "-preview"),
      t = ensureAudio()[kind];
    if (t.sourceStart < p.duration) p.currentTime = t.sourceStart;
    drawAudioTimeline();
  };
}
$("#music-loop").onchange = () => {
  if (!project) return;
  ensureAudio().music.loop = $("#music-loop").checked;
  changed();
  drawAudioTimeline();
};
$("#upload-audio").onclick = () => $("#upload-button").click();
$("#refresh-tools").onclick = action(async () => {
  if (!project?.sessionId) return;
  await save();
  const gen = epoch;
  $("#refresh-tools").disabled = true;
  try {
    const p = await api(path("/connect?refresh=1"), {});
    if (gen === epoch) {
      project.toolsVersion = p.toolsVersion;
      drawProject();
      toast("项目工具配置已更新");
    }
  } finally {
    $("#refresh-tools").disabled = false;
  }
});

function newsURL(group, file) {
  return path('/news/' + group + '/files/' + encodeURIComponent(file));
}
function drawNews() {
  const origin = project?.news;
  $('#news-panel').hidden = !origin;
  const player = $('#news-player');
  if (!origin) {
    player.pause(); player.removeAttribute('src'); player.load();
    delete player.dataset.source;
    return;
  }
  $('#news-summary').textContent = '原稿 r' + origin.revision + ' · ' + origin.packageKey + ' · 目标 ' + origin.targetDuration + ' 秒 · 原稿包完整性已校验';
  $('#news-source-download').href = newsURL('script', 'bundle.zip') + '?download';
  $('#news-video').hidden = !origin.video;
  $('#import-news-video').disabled = !!origin.video;
  if (origin.video) {
    const v = origin.video;
    $('#news-video-meta').textContent = '原始交接成片 · ' + v.width + ' × ' + v.height + ' · ' + v.fps + ' fps · ' + v.duration.toFixed(2) + ' 秒';
    const url = newsURL('video', 'video.mp4');
    if (player.dataset.source !== url) {
      player.pause(); player.src = url; player.poster = newsURL('video', 'cover.png'); player.dataset.source = url;
    }
    const links = $('#news-video-downloads'); links.replaceChildren();
    for (const [name, label] of [['video.mp4','MP4'],['narration.wav','配音 WAV'],['subtitles.srt','字幕 SRT'],['manifest.json','制作清单'],['bundle.zip','完整成片包']]) {
      const a = el('a', label); a.href = newsURL('video', name) + '?download'; links.append(a);
    }
  } else {
    player.pause(); player.removeAttribute('src'); player.load(); delete player.dataset.source;
  }
}
$('#import-news').onclick = () => $('#news-package').click();
$('#news-package').onchange = action(async () => {
  const file = $('#news-package').files[0];
  if (!file) return;
  $('#news-package').value = '';
  if (file.size > 4 * 1024 * 1024) throw new Error('脚本制作包最大 4 MiB');
  if (dirty) await save();
  const gen = epoch, data = new FormData(); data.append('file', file);
  $('#import-news').disabled = true;
  try {
    const p = await api('/api/news/import', data);
    // A project switch or new draft while uploading must not be overwritten.
    if (gen === epoch && !dirty) await openProject(p.id);
    await listProjects();
    toast('已接收审核稿；分镜为待核对的制作草稿。');
  } finally { $('#import-news').disabled = false; }
});
$('#import-news-video').onclick = () => $('#news-video-package').click();
$('#news-video-package').onchange = action(async () => {
  const file = $('#news-video-package').files[0];
  if (!file || !project?.news) return;
  $('#news-video-package').value = '';
  if (file.size > 256 * 1024 * 1024) throw new Error('成片包最大 256 MiB');
  if (dirty) await save();
  const gen = epoch, serial = editSerial, pid = project.id, data = new FormData();
  data.append('file', file); data.append('revision', String(project.revision));
  $('#import-news-video').disabled = true;
  try {
    const p = await api('/api/projects/' + pid + '/news/video', data);
    if (gen === epoch) {
      if (!dirty && serial === editSerial) { project = p; drawProject(); }
      else { $('#conflict').hidden = false; }
    }
    toast('原始成片已校验并接收。');
  } finally { if (gen === epoch) $('#import-news-video').disabled = !!project.news?.video; }
});
$('#news-close').onclick = () => $('#news-dialog').close();
$('#news-details').onclick = action(async () => {
  if (!project?.news) return;
  const gen = epoch, data = await api(path('/news'));
  if (gen !== epoch) return;
  const box = $('#news-evidence'); box.replaceChildren();
  box.append(el('h3', data.snapshot.document.title), el('p', '原稿 r' + data.origin.revision + ' · 内容 SHA-256：' + data.origin.contentHash), el('pre', data.snapshot.document.script_text));
  box.append(el('h3', '所选来源'));
  for (const source of data.sources) {
    const article = el('article'); article.append(el('strong', source.title || source.article_key));
    try {
      const u = new URL(source.url);
      if (['http:', 'https:'].includes(u.protocol) && !u.username && !u.password) {
        const link = el('a', ' 打开原文'); link.href = u.href; link.target = '_blank'; link.rel = 'noopener noreferrer'; article.append(link);
      }
    } catch (_) {}
    article.append(el('p', source.published_at || ''), el('pre', JSON.stringify(source, null, 2))); box.append(article);
  }
  const reviews = el('details'); reviews.append(el('summary', '原稿审核记录'), el('pre', JSON.stringify(data.manifest.review_history, null, 2))); box.append(reviews);
  $('#news-dialog').showModal();
});

function srtTime(seconds) {
  const ms = Math.round(seconds * 1000);
  const pad = (x, n=2) => String(x).padStart(n,'0');
  return pad(Math.floor(ms/3600000)) + ':' + pad(Math.floor(ms/60000)%60) + ':' + pad(Math.floor(ms/1000)%60) + ',' + pad(ms%1000,3);
}
function currentSRT() {
  return (project?.subtitles?.cues || []).map((c,i) => `${i+1}\n${srtTime(c.start)} --> ${srtTime(c.end)}\n${c.text}\n`).join('\n');
}
function drawSubtitleState() {
  const t = project?.subtitles;
  const states = {ready:'可渲染', stale:'需要重新核对', unreviewed:'待核对', disabled:'已关闭'};
  $('#subtitle-state').textContent = dirty ? '有未保存的编辑；保存后会重新检查字幕与配音绑定。' : t ? (states[t.status] || t.status) + ' · ' + t.reason : '未设置字幕';
  $('#subtitle-state').dataset.state = dirty ? 'unreviewed' : t?.status || 'disabled';
}
function drawSubtitles() {
  if (!project) return;
  $('#subtitle-srt').value = captionDraft?.srt ?? currentSRT();
  $('#subtitles-enabled').checked = captionDraft?.enabled ?? !!project.subtitles?.enabled;
  $('#download-subtitles').hidden = !project.subtitles;
  $('#download-subtitles').href = path('/subtitles.srt');
  $('#subtitle-seek').max = project.shots.reduce((n,s)=>n+s.duration,0) || 1;
  showSubtitleAt(Number($('#subtitle-seek').value));
  drawSubtitleState();
}
function showSubtitleAt(time) {
  $('#subtitle-seek').value = time;
  $('#subtitle-time').textContent = time.toFixed(3) + ' 秒';
  const cue = project?.subtitles?.cues.find(c => time >= c.start && time < c.end);
  $('#subtitle-live').textContent = cue?.text || '此处无字幕';
}
function captionChanged() {
  if (!project) return;
  captionDraft = {srt:$('#subtitle-srt').value, enabled:$('#subtitles-enabled').checked};
  changed();
}
$('#subtitle-srt').oninput = captionChanged;
$('#subtitles-enabled').onchange = captionChanged;
$('#save-subtitles').onclick = action(save);
$('#load-srt').onclick = () => { if (project) $('#srt-file').click(); };
$('#srt-file').onchange = action(async () => {
  const file = $('#srt-file').files[0]; $('#srt-file').value = '';
  if (!file || !project) return;
  if (file.size > 128*1024) throw new Error('SRT 最大 128 KiB');
  const gen = epoch, serial = editSerial, text = new TextDecoder('utf-8', {fatal:true}).decode(await file.arrayBuffer());
  if (gen !== epoch || serial !== editSerial) return;
  $('#subtitle-srt').value = text; $('#subtitles-enabled').checked = true; captionChanged();
});
async function subtitleMutation(tail, body, method) {
  if (!project) return;
  const gen = epoch;
  await save();
  if (gen !== epoch || dirty) throw new Error('项目仍有新编辑，请先保存后重试');
  const serial = editSerial, pid = project.id;
  const payload = {...body, revision:project.revision};
  if (tail === '/subtitles') payload.srt = currentSRT();
  const p = await api('/api/projects/' + pid + tail, payload, method);
  if (gen !== epoch) return;
  if (serial !== editSerial || dirty) { $('#conflict').hidden = false; return; }
  project = p; drawProject();
}
$('#review-subtitles').onclick = action(async () => {
  if (!project) return;
  if (!confirm('确认已经试听当前配音，并逐段核对字幕文字和时间？此操作记录人工核对，不会生成新配音。')) return;
  await subtitleMutation('/subtitles', {enabled:true, reviewed:true}, 'PUT');
});
$('#adopt-news-audio').onclick = action(async () => {
  if (!project?.news?.video) return;
  if (!confirm('将替换当前配音与字幕，并按原片配音总长缩放镜头。镜头文字和素材保留，切点仍需检查。继续？')) return;
  await subtitleMutation('/news/audio', {}, 'POST');
});
$('#subtitle-seek').oninput = () => {
  const time = Number($('#subtitle-seek').value); showSubtitleAt(time);
  const audio = $('#narration-preview'), track = project?.audio?.narration;
  if (track?.assetId && Number.isFinite(audio.duration)) audio.currentTime = Math.max(0, Math.min(audio.duration, time-track.start+track.sourceStart));
};
$('#narration-preview').addEventListener('timeupdate', () => {
  const track = project?.audio?.narration;
  if (track?.assetId) showSubtitleAt(Math.max(0,$('#narration-preview').currentTime - track.sourceStart + track.start));
});

function drawSpeech() {
  if (!project) return;
  const jobs = project.speech || [];
  const active = jobs.find(j => ['running','stopping'].includes(j.state));
  let pending = null, storageError = '';
  try { pending = speechSubmission.reconcile(project.id, jobs); } catch (e) { storageError = e.message; }
  $('#speech-start').disabled = startingSpeech || savingTTS || !!active || !ttsSettings || !!storageError;
  $('#speech-start').textContent = pending ? '确认上次配音请求' : '生成配音与字幕';
  $('#tts-save').disabled = savingTTS || !!active || !!ttsSettings?.managedByEnvironment;
  $('#speech-state').textContent = storageError || (pending ? '上次提交结果待确认；点击确认将沿用原稿件版本和配音参数，不创建重复任务。' : active ? `配音 ${active.progress}/${active.total} 段 · ${states[active.state] || active.state}` : '生成结果不会自动替换当前音轨。');
  const key = project.id + ':' + JSON.stringify(jobs) + ':' + project.audio?.narration?.assetId;
  if (key === speechKey) return;
  speechKey = key;
  const box = $('#speech-jobs'); for (const audio of box.querySelectorAll('audio')) audio.pause(); box.replaceChildren();
  if (!jobs.length) { box.append(el('p','尚无自动配音任务。','muted small')); return; }
  for (const j of [...jobs].reverse()) {
    const row = el('article',undefined,'speech-job'), head = el('div'), links = el('div',undefined,'row subtitle-actions');
    head.append(el('strong', `修订 ${j.revision} · ${states[j.state] || j.state} · ${j.backend === "indextts" ? "IndexTTS / " + j.language + " / " + j.speed + "×" : "eSpeak / " + j.rate} · ${j.voice}`));
    head.append(el('p', `${j.progress}/${j.total} 段` + (j.duration ? ` · ${j.duration.toFixed(3)} 秒` : ''),'muted small'));
    if (j.error) head.append(el('p',j.error,'render-error'));
    const pid = project.id, gen = epoch, base = '/api/projects/' + pid + '/speech/' + j.id;
    if (j.state === 'completed') {
      const audio = el('audio'); audio.controls = true; audio.preload = 'none'; audio.src = base + '/files/narration.wav'; head.append(audio);
      const use = el('button', project.audio?.narration?.assetId === j.id ? '重新采用此配音' : '采用配音与字幕');
      use.onclick = action(async () => {
        if (gen !== epoch) return;
        if (!confirm('采用此任务会替换当前配音和字幕，并按实测音频调整各镜头时长。已试听并继续？')) return;
        use.disabled = true;
        try { await subtitleMutation('/speech/' + j.id + '/adopt',{},'POST'); }
        finally { use.disabled = false; }
      }); links.append(use);
      for (const [file,label] of [['narration.wav','WAV'],['subtitles.srt','SRT'],['manifest.json','配音清单']]) {
        const a = el('a',label); a.href = base + '/files/' + file + '?download'; links.append(a);
      }
    }
    if (j.state === 'running') {
      const stop = el('button','取消配音');
      stop.onclick = action(async()=>{stop.disabled=true;try{await api(base+'/stop',{});await poll();}finally{stop.disabled=false;}}); links.append(stop);
    }
    const log = el('a','日志'); log.href = base + '/files/speech.log'; log.target='_blank'; log.rel='noopener'; links.append(log);
    row.append(head,links);box.append(row);
  }
}
function updateSpeechOptions(resetVoice = false) {
  const neural = $('#speech-backend').value === 'indextts';
  $('#speech-voice').setAttribute('list', neural ? 'tts-voices' : 'speech-voices');
  $('#speech-rate-field').hidden = neural;
  $('#speech-speed-field').hidden = !neural;
  $('#speech-language-field').hidden = !neural;
  if (resetVoice) $('#speech-voice').value = neural ? (ttsSettings?.voice || '') : 'cmn';
}
function applyTTSSettings(c) {
  ttsSettings = c;
  for (const [id, key] of [['backend','backend'],['url','url'],['model','model'],['voice','voice'],['language','language'],['timeout','timeoutSeconds']]) {
    $('#tts-'+id).value = c[key] ?? '';
  }
  $('#tts-key').value = ''; $('#tts-clear-key').checked = false;
  $('#tts-key').placeholder = c.hasApiKey ? '已保存；留空保持' : '服务无认证时可留空';
  for (const n of $('#tts-settings').querySelectorAll('input,select,button')) n.disabled = c.managedByEnvironment;
  $('#tts-settings-state').textContent = c.managedByEnvironment ? '由环境变量管理，修改启动配置后重启生效。' : '保存后立即生效，已有任务和产物保留。';
  $('#speech-backend').value = c.backend;
  $('#speech-language').value = c.language;
  updateSpeechOptions(true);
  $('#speech-environment').textContent = c.backend === 'indextts' ? '已选择 IndexTTS；点击环境检查验证服务，首次生成请先试听。' : 'eSpeak 仅用于机械音预览；正式配音可在服务设置中切换 IndexTTS。';
  $('#tts-discover').disabled = discoveringVoices;
  $('#tts-voices').replaceChildren();
  $('#speech-voice').setAttribute('list', c.backend === 'indextts' ? 'tts-voices' : 'speech-voices');
  drawSpeech();
}
async function loadTTSSettings() {
  applyTTSSettings(await api('/api/speech/settings'));
}
function ttsDraft() {
  return {
    backend: $('#tts-backend').value, url: $('#tts-url').value.trim(), model: $('#tts-model').value.trim(),
    voice: $('#tts-voice').value.trim(), language: $('#tts-language').value, timeoutSeconds: Number($('#tts-timeout').value),
    apiKey: $('#tts-key').value, clearApiKey: $('#tts-clear-key').checked
  };
}
$('#tts-url').addEventListener('input', () => $('#tts-voices').replaceChildren());
$('#tts-discover').onclick = action(async () => {
  if (discoveringVoices) return;
  discoveringVoices = true; $('#tts-discover').disabled = true;
  const draft = ttsDraft(), url = draft.url;
  $('#tts-voices').replaceChildren();
  $('#tts-settings-state').textContent = '读取音色列表中…';
  try {
    const catalog = await api('/api/speech/voices', ttsSettings?.managedByEnvironment ? undefined : draft);
    if ($('#tts-url').value.trim() !== url) return;
    for (const voice of catalog.voices) {
      const option = el('option'); option.value = voice; $('#tts-voices').append(option);
    }
    $('#tts-settings-state').textContent = catalog.supported
      ? (catalog.voices.length ? `已读取 ${catalog.voices.length} 个音色，请在音色输入框选择。` : '服务音色列表为空，请先在服务端注册音色。')
      : catalog.message;
  } catch (e) { $('#tts-settings-state').textContent = e.message; throw e; }
  finally { discoveringVoices = false; $('#tts-discover').disabled = false; }
});
$('#speech-backend').onchange = () => updateSpeechOptions(true);
$('#tts-save').onclick = action(async () => {
  if (savingTTS) return;
  savingTTS = true; $('#tts-save').disabled = true; drawSpeech();
  try {
    const c = await api('/api/speech/settings', ttsDraft(), 'PUT');
    applyTTSSettings(c); toast('配音设置已保存。');
  } finally { savingTTS = false; $('#tts-save').disabled = !!ttsSettings?.managedByEnvironment; drawSpeech(); }
});
$('#speech-check').onclick = action(async()=>{
  $('#speech-check').disabled = true;
  try {
    const c = await api('/api/speech/capabilities?backend='+encodeURIComponent($('#speech-backend').value));
    const missing = [!c.ffmpegReady && 'FFmpeg', !c.ffprobeReady && 'FFprobe', c.backend === 'espeak-ng' && !c.espeakReady && 'eSpeak NG'].filter(Boolean);
    $('#speech-environment').textContent = c.ready ? (c.backend === 'indextts' ? 'IndexTTS 模型接口可连接；音色与发音效果需通过实际生成验证。' : '本机预览工具已找到。') : [c.error, missing.length && '尚缺：'+missing.join('、')].filter(Boolean).join('；');
  } finally { $('#speech-check').disabled = false; }
});
$('#speech-start').onclick = action(async()=>{
  if (!project || startingSpeech) return;
  const gen = epoch, pid = project.id, voice = $('#speech-voice').value.trim(), backend = $('#speech-backend').value;
  const options = backend === 'indextts' ? {speed:Number($('#speech-speed').value),language:$('#speech-language').value} : {rate:Number($('#speech-rate').value)};
  let body = speechSubmission.read(pid);
  startingSpeech = true; drawSpeech();
  try {
    if (!body) {
      await save();
      if (gen !== epoch || dirty) throw new Error('项目仍有新编辑，请保存后重试');
      body = {revision:project.revision, requestId:id(), voice, backend, ...options};
    }
    await speechSubmission.submit(pid, body, request => api('/api/projects/' + pid + '/speech', request, 'POST', 30000));
    if (gen === epoch) {toast('配音任务已确认，完成后可试听并采用。');await poll();}
  } finally { startingSpeech = false; drawSpeech(); }
});

$('#new-session').onclick = action(async () => {
  if (!project?.sessionId) return;
  await save();
  const gen = epoch, pid = project.id, expected = project.sessionId;
  $('#new-session').disabled = true;
  try {
    await api(path('/connect?new=1&expectedSessionId='+encodeURIComponent(expected)), {});
    if (gen !== epoch) return;
    rememberMessage(pid, null);
    await openProject(pid);
    toast('已新建对话；原对话历史保留在 RunDesk。');
  } finally { $('#new-session').disabled = false; }
});

function drawProduction() {
  if (!project) return;
  if (!productionDirty) $('#production-text').value = project.production?.text ?? project.shots.map(s=>s.text).join('');
  const stage = project.production?.stage || 'draft';
  $('#production-stage').textContent = ({draft:'初稿',adjustment:'调整',final:'终稿'})[stage];
  const media = project.shots.filter(s=>s.assetId).length;
  $('#production-summary').textContent = `${project.shots.length} 段正文 · ${media} 段已配素材 · ${project.shots.length-media} 段文字卡。初稿不代表已核稿，终稿选用当前内容一致的已完成视频。`;
  const box = $('#alignment-table'); box.replaceChildren();
  const table = el('table'); const head=el('tr');
  for (const label of ['段落正文','图片 / 视频','时长']) head.append(el('th',label));
  table.append(head);
  project.shots.forEach((shot,index)=> {
    const tr=el('tr'), text=el('td'), visual=el('td'), time=el('td');
    text.append(el('small',String(index+1).padStart(2,'0')),el('p',shot.text));
    const asset=project.assets.find(a=>a.id===shot.assetId);
    if (asset?.mime.startsWith('image/')) { const img=el('img');img.src=assetURL(asset);img.alt=asset.name;img.loading='lazy';visual.append(img); }
    if (asset?.mime.startsWith('video/')) { const v=el('video');v.src=assetURL(asset);v.controls=true;v.preload='metadata';v.muted=true;visual.append(v); }
    const select=el('select'),empty=el('option','文字卡');empty.value='';select.append(empty);
    for(const a of project.assets.filter(a=>/^(image|video)\//.test(a.mime))) {const o=el('option',a.name);o.value=a.id;select.append(o)}
    select.value=shot.assetId;select.onchange=()=>{shot.assetId=select.value;changed();drawShots(true);drawProduction()};visual.append(select);
    const reason=el('input');reason.placeholder='素材与正文的对应依据（可选）';reason.maxLength=300;reason.value=shot.visualReason||'';
    reason.oninput=()=>{shot.visualReason=reason.value;changed()};visual.append(reason);
    const duration=el('input');duration.type='number';duration.min='1';duration.max='60';duration.step='0.1';duration.value=shot.duration;
    duration.onchange=()=>{shot.duration=Number(duration.value);changed();drawShots(true)};time.append(duration);
    tr.append(text,visual,time);table.append(tr);
  });
  box.append(table);
}
$('#production-text').oninput=()=>{productionDirty=true;changed()};
$('#apply-text').onclick=action(async()=>{productionDirty=true;changed();await save();toast('正文与素材对齐表已保存')});
$('#load-text').onclick=()=>$('#text-file').click();
$('#text-file').onchange=action(async()=>{const f=$('#text-file').files[0];if(!f)return;if(f.size>128*1024)throw Error('正文文件不能超过128KiB');$('#production-text').value=await f.text();productionDirty=true;changed();$('#text-file').value=''});
$('#ask-align').onclick=action(async()=>{
  if(!project)return;productionDirty=true;changed();await save();
  $('#prompt').value='请按已保存的新闻正文逐段安排现有图片和视频素材，保持事实、段落顺序和原文。先读取 project.production 和素材；用真实素材ID保存对齐结果，无合适素材用文字卡并说明。完成制作初稿；使用已配置的配音和渲染工具，不补充云端生成服务。';
  $('#prompt').focus();if(project.sessionId){$('#composer').requestSubmit()}else{toast('请先连接 RunDesk；制作要求已保留在对话栏')}
});
$('#adjust-production').onclick=action(async()=>{await save();project=await api(path('/production/stage'),{revision:project.revision,stage:'adjustment'});drawProject()});
$('#finalize-production').onclick=action(async()=>{
  await save();
  const completed=[...project.renders].reverse().filter(r=>r.state==='completed');
  if(!completed.length)throw Error('请先完成视频渲染并检查成片');
  // Server verifies exact content; never silently chooses an outdated video.
  project=await api(path('/production/stage'),{revision:project.revision,stage:'final',renderId:completed[0].id});
  renderKey='';drawProject();toast('当前成片已确认为终稿');
});
$('#upload-folder').onclick=()=>$('#folder-files').click();
$('#folder-files').onchange=action(async()=>{
  if(!project)throw Error('请先创建项目');await save();const pid=project.id,gen=epoch;
  for(const file of $('#folder-files').files) {
    if(!/\.(png|jpe?g|webp|mp4|webm|wav|mp3|m4a|flac|ogg)$/i.test(file.name))continue;
    const body=new FormData();body.append('file',file);const p=await api('/api/projects/'+pid+'/assets',body);
    if(gen===epoch){if(dirty){project.assets=p.assets;$('#conflict').hidden=false}else{project=p;drawProject()}}
  }
  $('#folder-files').value='';toast('目录素材已导入');
});

const capabilityLabels={media:'素材',tts:'配音',image_generation:'图片生成',video_generation:'视频生成',text_media_alignment:'描述匹配（基线）',editing:'编辑',rendering:'渲染'};
async function loadCapabilities(probe=false) {
  $('#capability-status').textContent=probe?'正在检查服务…':'正在读取能力…';
  try {
    const result=await api('/api/capabilities'+(probe?'?probe=1':''));
    const box=$('#capability-list');box.replaceChildren();
    for(const c of result.capabilities) {
      const row=el('div',undefined,'capability-row');
      row.append(el('strong',capabilityLabels[c.id]||c.id));
      const configured=c.providers.some(p=>p.state==='configured');
      row.append(el('span',!c.implemented?'未接入':c.ready?'检查通过':configured?'已配置，待探测':'未就绪'));
      for(const p of c.providers) {
        row.append(el('small',p.id+(p.default?' · 默认':'')+' · '+({available:'检查通过',configured:'已配置',blocked:'未就绪'})[p.state],'muted'));
        for(const reason of p.reasons)row.append(el('small',reason,'muted'));
      }
      for(const note of c.notes)row.append(el('small',note,'muted'));
      box.append(row);
    }
    $('#capability-status').textContent='检查通过表示依赖或模型列表可用；真实输出以制作任务结果为准。';
  }catch(e){$('#capability-status').textContent=e.message;throw e}
}
async function loadProductionJobs(force=false) {
  if(!project||serviceJobsLoading)return;
  const key=project.id+JSON.stringify([project.speech,project.renders]);if(!force&&key===serviceJobsKey)return;
  serviceJobsLoading=true;const pid=project.id,gen=epoch;
  try {
    const result=await api('/api/projects/'+pid+'/jobs');if(gen!==epoch)return;
    const box=$('#production-jobs');box.replaceChildren();
    if(!result.jobs.length)box.append(el('p','还没有配音或渲染任务。','muted small'));
    for(const j of result.jobs) {
      const row=el('div',undefined,'service-job');
      row.append(el('strong',(j.type==='tts'?'配音':'渲染')+' · '+({running:'运行中',cancel_requested:'正在取消',succeeded:'完成',failed:'失败',interrupted:'中断',unknown:'未知状态'})[j.state]));
      row.append(el('small','修订 '+j.revision+' · '+j.provider+(j.progressKnown?' · '+j.completedUnits+'/'+j.totalUnits:' · 未提供步骤进度'),'muted'));
      if(j.error)row.append(el('p',j.error,'render-error'));
      if(j.state==='running'){
        const stop=el('button','取消此任务');stop.onclick=action(async()=>{stop.disabled=true;try{await api('/api/projects/'+pid+'/jobs/'+j.jobId+'/cancel',{});if(gen===epoch){serviceJobsKey='';await loadProductionJobs(true)}}finally{stop.disabled=false}});row.append(stop);
      }
      for(const artifact of j.artifacts){const link=el('a',artifact.name);link.href=artifact.url;link.target='_blank';link.rel='noopener';row.append(link)}
      box.append(row);
    }
    serviceJobsKey=key;
  }finally{serviceJobsLoading=false}
}
$('#refresh-capabilities').onclick=action(()=>loadCapabilities(true));
$('#refresh-jobs').onclick=action(()=>loadProductionJobs(true));

function drawEditingMode(){
 const active=!!project?.editActive;
 $('#edit-mode').textContent=active?'剪辑 DSL 模式':'旧镜头模式';
 const canvas=project?.editDsl?.canvas||{};$('#preview-format').textContent=active?`${canvas.width||1080} × ${canvas.height||1920} · ${canvas.fps||30} fps`:'9:16 · 1080 × 1920 · 30 fps';
 $('#production-panel').hidden=active;
 $('#visual-details').hidden=active;
 $('#subtitle-section').hidden=active;
 $('#sound-section').parentElement.hidden=active;
 $('#save').textContent=active?'保存剪辑':'保存制作稿';
 $('#preview-label').textContent=active?'剪辑预览（以成片为准）':'镜头预览';
}
function drawEditTimeline(ir){
 const box=$('#edit-timeline');box.replaceChildren();if(!ir){box.append(el('p','尚未保存剪辑，可先创建示例或交给 RunDesk 编写。','muted small'));return}
 box.append(el('p',`${ir.canvas.width} × ${ir.canvas.height} · ${ir.canvas.fps} fps · ${ir.duration.toFixed(3)} 秒`,'muted small'));
 const tracks=new Map();for(const clip of ir.clips){if(!tracks.has(clip.track))tracks.set(clip.track,[]);tracks.get(clip.track).push(clip)}
 for(const [id,clips]of tracks){const lane=el('div',undefined,'edit-lane');lane.append(el('strong',id+' · '+clips[0].kind));const strip=el('div',undefined,'edit-strip');for(const clip of clips){const block=el('button',clip.id,'edit-block '+clip.kind);block.style.left=(clip.start/ir.duration*100)+'%';block.style.width=Math.max(1,clip.duration/ir.duration*100)+'%';block.title=clip.id+' · '+clip.start.toFixed(3)+'–'+clip.end.toFixed(3)+' 秒';block.onclick=()=>{$('#edit-selection').textContent='选中对象：'+clip.id;$('#edit-status').textContent='选中片段：'+clip.id+'，轨道：'+clip.track+'。可在对话中使用这个对象ID要求调整。'};strip.append(block)}lane.append(strip);box.append(lane)}
}
async function loadEditPanel(force=false){
 if(!project||editPanelLoading||editDSLDirty&&!force)return;
 editPanelLoading=true;const pid=project.id,gen=epoch;
 try{const data=await api('/api/projects/'+pid+'/edit');if(gen!==epoch)return;if(editDSLDirty&&!force)return;$('#edit-json').value=data.dsl?JSON.stringify(data.dsl,null,2):'';editDSLBase=data.revision;editDSLDirty=false;drawEditTimeline(data.ir);$('#edit-status').textContent=data.dsl?'剪辑已保存 · 修订 '+data.revision:'剪辑内容与正文结构独立。'}finally{editPanelLoading=false}
}
$('#edit-json').oninput=()=>{if(!editDSLDirty)editDSLBase=project.revision;editDSLDirty=true;$('#edit-status').textContent='有未保存剪辑'};
$('#edit-example').onclick=()=>{
 if(!project)return;
 const visual=project.assets.find(a=>a.mime.startsWith('image/'))||project.assets.find(a=>a.mime.startsWith('video/'));
 const dsl={version:1,canvas:{width:1080,height:1920,fps:30,background:'#11182a'},tracks:[{id:'visuals',kind:'video'},{id:'titles',kind:'text'}],clips:[]};
 if(visual)dsl.clips.push({id:'opening_visual',track:'visuals',asset:visual.id,start:0,duration:3,layout:{fit:'cover'},fadeIn:.2});
 dsl.clips.push({id:'opening_title',track:'titles',start:0,duration:3,text:project.name,fontSize:64,layout:{x:60,y:120,width:960,height:300},fadeIn:.2});
 $('#edit-json').value=JSON.stringify(dsl,null,2);editDSLBase=project.revision;editDSLDirty=true;$('#edit-status').textContent='示例尚未保存。视频素材需先核对实际长度；源音默认静音。';
};
$('#edit-validate').onclick=action(async()=>{const dsl=JSON.parse($('#edit-json').value);const result=await api(path('/edit/validate'),{dsl});drawEditTimeline(result.ir);$('#edit-status').textContent='校验通过，尚未保存或渲染'});
async function saveEditDSL(){
 if(!project)return;const pid=project.id,gen=epoch,dsl=JSON.parse($('#edit-json').value);
 const result=await api('/api/projects/'+pid+'/edit',{revision:editDSLBase,dsl},'PUT');if(gen!==epoch)return;
 editDSLDirty=false;editDSLBase=result.revision;const current=await api('/api/projects/'+pid);if(gen!==epoch)return;project=current;drawProject();drawEditTimeline(result.ir);toast('剪辑已保存并启用，下一次生成使用这份时间线');
}
$('#edit-save').onclick=action(saveEditDSL);
$('#edit-reload').onclick=action(async()=>{if(editDSLDirty&&!confirm('放弃未保存剪辑并载入？'))return;editDSLDirty=false;await loadEditPanel(true)});
async function switchEditing(active){if(editDSLDirty)throw Error('请先保存剪辑或载入已保存版本');await save();const gen=epoch,pid=project.id;const p=await api('/api/projects/'+pid+'/edit/active',{revision:project.revision,active});if(gen!==epoch)return;project=p;drawProject()}
$('#edit-activate').onclick=action(()=>switchEditing(true));$('#edit-legacy').onclick=action(()=>switchEditing(false));

$("#narration-text").oninput=()=>{narrationDirty=true;};
$("#narration-save").onclick=action(async()=>{const pid=project.id,gen=epoch;const result=await api("/api/projects/"+pid+"/narration",{revision:narrationBase,text:$("#narration-text").value},"PUT");if(epoch!==gen||project.id!==pid)return;project=result;narrationDirty=false;drawProject();$("#narration-status").textContent="正文已保存，配音需重新核对";});

let workflowDirty=false,workflowBase=0,workflowProject="";
function drawWorkflow(){
 if(!project)return;
 if(workflowProject!==project.id){workflowDirty=false;workflowProject=project.id;}
 if(!workflowDirty){workflowBase=project.revision;const w=project.workflow||{skills:['video-narration','video-editing','video-review'],instructions:''};
 for(const name of ['narration','editing','review']) $('#skill-'+name).checked=w.skills.includes('video-'+name);
 $('#workflow-instructions').value=w.instructions||'';}
 $('#draft-start').disabled=!project.sessionId||sending;
}
for(const name of ['narration','editing','review']) $('#skill-'+name).onchange=()=>{workflowDirty=true;};
$('#workflow-instructions').oninput=()=>{workflowDirty=true;};
async function saveWorkflow(){
 const pid=project.id,gen=epoch;const workflow={skills:['narration','editing','review'].filter(n=>$('#skill-'+n).checked).map(n=>'video-'+n),instructions:$('#workflow-instructions').value};
 const result=await api('/api/projects/'+pid+'/workflow',{revision:workflowBase,workflow},'PUT');
 if(gen!==epoch||project.id!==pid)return false;project=result;workflowDirty=false;drawProject();$('#workflow-status').textContent='制作要求已保存';return true;
}
$('#workflow-save').onclick=action(saveWorkflow);
$('#draft-start').onclick=action(async()=>{
 if(!project?.sessionId||sending)return;const pid=project.id,gen=epoch;
 if(pendingMessage(pid))throw new Error('上一条提交仍待确认，请先恢复原提交');
 if(session&&['running','waiting','starting','stopping'].includes(session.status))throw new Error('当前 Agent 任务尚未结束，请用对话调整或先停止');
 if($('#prompt').value.trim())throw new Error('对话框中有未发送内容，请先发送或清空');
 if(narrationDirty)throw new Error('请先保存口播正文');
 if(workflowDirty)throw new Error('请先保存制作要求');
 await save();if(gen!==epoch||project.id!==pid)return;
 const instruction=await api('/api/projects/'+pid+'/draft/instruction',{revision:project.revision});
 if(gen!==epoch||project.id!==pid)return;
 $('#prompt').value=instruction.text;
 await submitMessage({preventDefault(){}});
 if(gen===epoch)$('#workflow-status').textContent='已交给 RunDesk；任务与取消状态见当前对话';
});

let patchPreview=null;
$('#edit-patch').oninput=()=>{patchPreview=null;$('#patch-apply').disabled=true;};
$('#patch-preview').onclick=action(async()=>{
 if(editDSLDirty)throw new Error('请先保存完整剪辑草稿');
 const pid=project.id,gen=epoch,body={revision:project.revision,changes:JSON.parse($('#edit-patch').value)};
 const result=await api('/api/projects/'+pid+'/edit/patch/preview',body);if(gen!==epoch||JSON.stringify(JSON.parse($('#edit-patch').value))!==JSON.stringify(body.changes))return;
 patchPreview={pid,body};$('#patch-apply').disabled=false;$('#patch-diff').textContent=JSON.stringify({fieldDiffs:result.fieldDiffs,affectedClipIds:result.affectedClipIds},null,2);
});
$('#patch-apply').onclick=action(async()=>{
 if(!patchPreview||patchPreview.pid!==project.id)throw new Error('请先预览本项目的修改');
 if(editDSLDirty)throw new Error('请先处理完整剪辑草稿');
 const pid=project.id,gen=epoch;await api('/api/projects/'+pid+'/edit/patch/apply',patchPreview.body);patchPreview=null;$('#patch-apply').disabled=true;
 if(gen!==epoch)return;const refreshed=await api('/api/projects/'+pid);if(gen!==epoch)return;project=refreshed;drawProject();await loadEditPanel(true);
});
$('#patch-undo').onclick=action(async()=>{
 if(editDSLDirty)throw new Error('请先处理完整剪辑草稿');const pid=project.id,gen=epoch;
 await api('/api/projects/'+pid+'/edit/undo',{revision:project.revision});if(gen!==epoch)return;
 const result=await api('/api/projects/'+pid);if(gen!==epoch)return;project=result;patchPreview=null;$('#patch-apply').disabled=true;drawProject();await loadEditPanel(true);
});

$('#patch-redo').onclick=action(async()=>{
 if(editDSLDirty)throw new Error('请先处理完整剪辑草稿');const pid=project.id,gen=epoch;
 await api('/api/projects/'+pid+'/edit/redo',{revision:project.revision});if(gen!==epoch)return;
 const result=await api('/api/projects/'+pid);if(gen!==epoch)return;project=result;patchPreview=null;$('#patch-apply').disabled=true;drawProject();await loadEditPanel(true);
});

$('#range-render').onclick=action(async()=>{
 if(editDSLDirty)throw new Error('请先保存剪辑草稿');if(startingRender)return;const pid=project.id,gen=epoch;
 const pending=renderSubmission.read(pid);if(pending&&!pending.options?.range)throw new Error('完整渲染提交仍待确认，请先确认原任务');
 const body=pending||{type:'render',revision:project.revision,requestId:id(),options:{kind:'draft',range:{start:Number($('#preview-start').value),end:Number($('#preview-end').value)}}};
 startingRender=true;let job;try{job=await renderSubmission.submit(pid,body,original=>api('/api/projects/'+pid+'/jobs',original));}finally{startingRender=false}if(gen!==epoch)return;$('#final-status').textContent='局部预览任务：'+job.nativeId+'；不能用于终稿';await loadProductionJobs(true);
});
$('#final-designate').onclick=action(async()=>{
 if(editDSLDirty||narrationDirty)throw new Error('请先处理未保存内容');const pid=project.id,gen=epoch;
 const result=await api('/api/projects/'+pid+'/final',{revision:project.revision,renderId:$('#final-render-id').value.trim(),reviewed:$('#final-reviewed').checked});if(gen!==epoch)return;
 const refreshed=await api('/api/projects/'+pid);if(gen!==epoch)return;project=refreshed;drawProject();$('#final-reviewed').checked=false;$('#final-status').textContent='已保存终稿快照：'+result.final.id;
});

$('#final-verify').onclick=action(async()=>{const pid=project.id,gen=epoch;$('#final-status').textContent='正在读取素材和成片进行复核…';const result=await api('/api/projects/'+pid+'/final/verify');if(gen!==epoch)return;$('#final-status').textContent='输入仍有效：'+(result.inputsCurrent?'是':'否')+'；成片与快照完整：'+(result.outputIntact?'是':'否');});
