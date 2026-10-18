"use strict";
const TOOL_VERSION = "0.8.0";
const $ = (s) => document.querySelector(s),
  el = (tag, text, cls) => {
    const n = document.createElement(tag);
    if (text !== undefined) n.textContent = text;
    if (cls) n.className = cls;
    return n;
  };
let ttsSettings = null, savingTTS = false;
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
const api = async (path, body, method) => {
  const opts = { method: method || (body === undefined ? "GET" : "POST") };
  if (body !== undefined) {
    opts.body = body instanceof FormData ? body : JSON.stringify(body);
    if (!(body instanceof FormData))
      opts.headers = { "Content-Type": "application/json" };
  }
  const r = await fetch(path, opts);
  const data = await r.json();
  if (!r.ok) {
    const e = new Error(data.error || r.statusText);
    e.status = r.status;
    e.code = data.code;
    e.uncertain = data.uncertain;
    e.requestId = data.requestId;
    throw e;
  }
  return data;
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
  selected = 0;
  dirty = false;
  after = 0;
  session = null;
  rows = new Map();
  renderKey = "";
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
function drawProject() {
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
    $("#" + name).disabled = !s;
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
  try {
    let p = await api('/api/projects/' + pid, project, "PUT");
    if (gen === epoch) project.revision = p.revision;
    if (captions) {
      p = await api('/api/projects/' + pid + '/subtitles', {...captions, revision:p.revision, reviewed:false}, 'PUT');
    }
    if (gen !== epoch) return p;
    if (serial === editSerial) {
      project = p;
      captionDraft = null;
      dirty = false;
      drawProject();
    } else {
      project.revision = p.revision;
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
      el("strong", "修订 " + r.revision + " · " + (states[r.state] || r.state)),
      el(
        "small",
        new Date(r.created).toLocaleString() +
          " · " +
          r.duration +
          " 秒" +
          (r.hasAudio ? " · 含音轨" : " · 无声"),
      ),
    );
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
  if (dirty && !confirm("放弃当前未保存编辑并切换项目？")) {
    $("#projects").value = project.id;
    return;
  }
  await openProject(pid);
});
$("#save").onclick = action(save);
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
$("#show-storyboard").onclick = showPoster;
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
  if (!project) return;
  await save();
  await api(path("/render"), {});
  toast("渲染任务已创建，可以继续编辑");
  await poll();
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
$("#composer").onsubmit = action(async (e) => {
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
    body = {text, requestId: id()};
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
});
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
  if (dirty) {
    e.preventDefault();
    e.returnValue = "";
  }
});
async function boot() {
  const list = await listProjects();
  showApp();
  await loadTTSSettings();
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
  $('#speech-start').disabled = startingSpeech || savingTTS || !!active || !ttsSettings;
  $('#tts-save').disabled = savingTTS || !!active || !!ttsSettings?.managedByEnvironment;
  $('#speech-state').textContent = active ? `配音 ${active.progress}/${active.total} 段 · ${states[active.state] || active.state}` : '生成结果不会自动替换当前音轨。';
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
  drawSpeech();
}
async function loadTTSSettings() {
  applyTTSSettings(await api('/api/speech/settings'));
}
$('#speech-backend').onchange = () => updateSpeechOptions(true);
$('#tts-save').onclick = action(async () => {
  if (savingTTS) return;
  savingTTS = true; $('#tts-save').disabled = true; drawSpeech();
  try {
    const c = await api('/api/speech/settings', {
      backend: $('#tts-backend').value, url: $('#tts-url').value.trim(), model: $('#tts-model').value.trim(),
      voice: $('#tts-voice').value.trim(), language: $('#tts-language').value, timeoutSeconds: Number($('#tts-timeout').value),
      apiKey: $('#tts-key').value, clearApiKey: $('#tts-clear-key').checked
    }, 'PUT');
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
  startingSpeech = true; drawSpeech();
  try {
    await save();
    if (gen !== epoch || dirty) throw new Error('项目仍有新编辑，请保存后重试');
    await api('/api/projects/' + pid + '/speech',{revision:project.revision, requestId:id(), voice, backend, ...options});
    if (gen === epoch) {toast('配音任务已创建，完成后可试听并采用。');await poll();}
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
