"use strict";

// Editorial metadata is an observation of a saved version, never a second editor.
let reviewRequest = 0, reviewLoading = false, reviewKey = "", reviewData = null;
let reviewClip = null, reviewQuote = "", reviewPosition = 0, reviewNarration = "";

function reviewSeconds(value) {
  const seconds = Math.max(0, Number(value) || 0), minutes = Math.floor(seconds / 60);
  return String(minutes).padStart(2, "0") + ":" + (seconds % 60).toFixed(3).padStart(6, "0");
}

function resetReviewTimeline() {
  reviewRequest++; reviewLoading = false; reviewKey = ""; reviewData = null;
  reviewClip = null; reviewQuote = ""; reviewPosition = 0; reviewNarration = "";
  $("#review-version").replaceChildren();
  const current = el("option", "当前工程"); current.value = ""; $("#review-version").append(current);
  $("#review-player").pause(); $("#review-player").removeAttribute("src"); $("#review-player").hidden = true;
  $("#review-timeline").replaceChildren(); $("#review-text").replaceChildren();
  $("#review-warnings").replaceChildren(); $("#hyperframes-files").replaceChildren();
  $("#hyperframes-project").hidden = true; $("#review-select-text").disabled = true;
  $("#review-no-video").hidden = false; $("#review-no-video").textContent = "当前工程尚无对应的已完成成片。时间线展示已保存的数据。";
  $("#review-selection").replaceChildren(el("p", "选择片段查看时间和原文引用。这里提供只读审阅，修改交给 RunDesk。", "muted small"));
  $("#review-status").textContent = "正在加载作品…";
  updateReviewPosition(0);
}

function drawReviewVersions() {
  const select = $("#review-version"), selected = select.value;
  select.replaceChildren();
  const current = el("option", "当前工程 · 修订 " + project.revision); current.value = ""; select.append(current);
  for (const render of [...(project.renders || [])].reverse()) {
    if (render.state !== "completed") continue;
    const confirmed = project.final?.renderId === render.id || project.production?.finalRenderId === render.id;
    const option = el("option", `${confirmed ? "已确认终稿记录" : render.range ? "局部预览" : "已渲染版本"} · 修订 ${render.revision} · ${editingBackendLabel(render.engine)} · ${render.id}`);
    option.value = render.id; select.append(option);
  }
  select.value = [...select.options].some(option => option.value === selected) ? selected : "";
}

function reviewTrackKind(track) {
  const kind = String(track.kind || "").toLowerCase();
  if (["visual", "video", "image", "animation"].includes(kind)) return "visual";
  if (["subtitle", "subtitles", "caption", "text"].includes(kind)) return "subtitle";
  if (kind === "music") return "music";
  if (["narration", "voice", "audio", "speech"].includes(kind)) return "narration";
  return "visual";
}

function drawReviewTimeline(data) {
  if (!project || data.projectId !== project.id) throw Error("作品时间线不属于当前项目");
  reviewData = data; reviewClip = null; reviewQuote = "";
  const historical = !!data.renderId;
  const source = {authored:"Agent 声明的剪辑信息", compiled:"编译后的剪辑信息", speech_chunks:"实际配音分段", archived_edl:"归档的剪辑信息", legacy_storyboard:"旧工程镜头信息"}[data.source] || "已保存的剪辑信息";
  $("#review-status").textContent = `${historical ? "历史版本" : data.current === false ? "当前工程（需核对）" : "当前工程"} · 修订 ${data.revision} · ${editingBackendLabel(data.backend)} · ${source}。${data.summary || "渲染完成不代表已人工确认。"}`;
  const warnings = $("#review-warnings"); warnings.replaceChildren();
  for (const warning of data.warnings || []) warnings.append(el("p", typeof warning === "string" ? warning : warning.message || JSON.stringify(warning), "review-warning small"));
  if (!historical && data.revision !== project.revision) warnings.append(el("p", "工程已有新修订，请刷新后再添加定位引用。", "review-warning small"));

  const render = (project.renders || []).find(item => item.id === data.renderId && item.state === "completed");
  const player = $("#review-player"), url = render ? "/api/projects/" + encodeURIComponent(project.id) + "/renders/" + encodeURIComponent(render.id) + "/final.mp4" : "";
  if (url) {
    if (player.dataset.reviewURL !== url) { player.pause(); player.src = url; player.dataset.reviewURL = url; }
    player.hidden = false; $("#review-no-video").hidden = true;
  } else {
    player.pause(); player.removeAttribute("src"); delete player.dataset.reviewURL; player.hidden = true;
    $("#review-no-video").hidden = false;
    $("#review-no-video").textContent = data.renderId ? "此版本没有可播放的已完成视频。" : "当前工程尚无对应的已完成成片。可切换到已渲染版本试听审阅。";
  }
  const narration = typeof data.text === "string" ? data.text : historical ? "" : project.narrationText || project.production?.text || "";
  reviewNarration = narration;
  drawReviewText([], historical ? "此历史快照未提供正文，不能使用当前正文替代。" : "尚未保存正文。");
  $("#review-select-text").disabled = true;
  const timeline = $("#review-timeline"); timeline.replaceChildren();
  const duration = Number(data.duration);
  const usableDuration = Number.isFinite(duration) && duration > 0;
  const kinds = [["visual", "画面 / 动画"], ["narration", "配音"], ["subtitle", "字幕"], ["music", "音乐"]];
  const allTracks = Array.isArray(data.tracks) ? data.tracks : [];
  for (const [kind, label] of kinds) {
    const tracks = allTracks.filter(track => reviewTrackKind(track) === kind);
    if (!tracks.length) tracks.push({id:kind, clips:[]});
    for (const track of tracks) {
      const lane = el("div", undefined, "review-lane"); lane.dataset.kind = kind;
      lane.append(el("strong", label + (track.label ? " · " + track.label : ""), "review-lane-label"));
      const strip = el("div", undefined, "review-strip"), clips = Array.isArray(track.clips) ? track.clips : [];
      let validCount = 0;
      for (const clip of clips) {
        const start = Number(clip.start), end = Number(clip.end);
        if (!usableDuration || !Number.isFinite(start) || !Number.isFinite(end) || start < 0 || end <= start || start >= duration) continue;
        validCount++;
        const button = el("button", clip.label || clip.text || clip.id || "片段", "review-block");
        button.type = "button"; button.dataset.clipId = clip.id || "";
        button.style.left = (start / duration * 100) + "%";
        button.style.width = (Math.min(end, duration) - start) / duration * 100 + "%";
        button.title = `${clip.label || clip.id || "片段"} · ${reviewSeconds(start)}–${reviewSeconds(end)}`;
        button.onclick = () => selectReviewClip(track, clip, button);
        strip.append(button);
      }
      if (!validCount) strip.append(el("span", usableDuration ? "未提供此轨道数据" : "未提供可用时长，无法绘制时间位置", "review-lane-empty"));
      const playhead = el("span", undefined, "review-playhead"); playhead.setAttribute("aria-hidden", "true"); strip.append(playhead);
      lane.append(strip); timeline.append(lane);
    }
  }
  $("#review-selection").replaceChildren(el("p", "选择片段查看时间和原文引用。这里提供只读审阅，修改交给 RunDesk。", "muted small"));
  $("#review-seek").disabled = !usableDuration; $("#review-seek").max = usableDuration ? String(duration) : "0";
  applyReviewZoom(); updateReviewPosition(Math.min(reviewPosition, usableDuration ? duration : 0));
}

function updateReviewPosition(value) {
  const duration = Number(reviewData?.duration) || 0;
  reviewPosition = Math.min(Math.max(0, Number(value) || 0), duration);
  $("#review-seek").value = String(reviewPosition);
  $("#review-position").textContent = reviewSeconds(reviewPosition) + " / " + reviewSeconds(duration);
  for (const node of document.querySelectorAll(".review-playhead")) node.style.left = (duration > 0 ? reviewPosition / duration * 100 : 0) + "%";
}

function seekReviewPosition(value) {
  updateReviewPosition(value);
  const player = $("#review-player");
  if (!player.hidden && player.getAttribute("src")) {
    // Range renders use local playback time while editorial metadata uses project time.
    const render = (project?.renders || []).find(item => item.id === reviewData?.renderId);
    const offset = Number(render?.range?.start) || 0;
    if (render?.range && (reviewPosition < offset || reviewPosition > Number(render.range.end))) {
      player.pause();
      $("#review-status").textContent = `所选位置不在此局部预览的 ${reviewSeconds(offset)}–${reviewSeconds(render.range.end)} 范围内。请选择完整成片审阅。`;
      return;
    }
    player.currentTime = Math.max(0, reviewPosition - offset);
  }
}

function selectReviewClip(track, clip, button) {
  reviewClip = {track, clip}; reviewQuote = "";
  for (const node of document.querySelectorAll(".review-block")) node.classList.toggle("selected", node === button);
  seekReviewPosition(clip.start);
  const box = $("#review-selection"); box.replaceChildren();
  box.append(el("strong", clip.label || clip.id || "片段"));
  box.append(el("p", `${track.label || track.id} · ${reviewSeconds(clip.start)}–${reviewSeconds(clip.end)}${clip.assetId ? " · 素材 " + clip.assetId : ""}`, "muted small"));
  const refs = Array.isArray(clip.textRefs) ? clip.textRefs.filter(ref => typeof ref.quote === "string" && ref.quote) : [];
  drawReviewText(refs);
  if (refs.length) {
    for (const ref of refs) box.append(el("blockquote", ref.quote));
  } else box.append(el("p", "此片段没有已记录的正文关联，不根据素材名称推断。", "muted small"));
  const add = el("button", "将片段引用加入调整草稿"); add.type = "button";
  add.onclick = action(() => appendReviewReference({track, clip})); box.append(add);
}

function drawReviewText(refs, missing = "尚未保存正文。") {
  const paragraph = el("p"), ranges = [];
  for (const ref of refs) {
    const runes = [...reviewNarration];
    if (Number.isInteger(ref.startRune) && Number.isInteger(ref.endRune) && ref.startRune >= 0 && ref.endRune > ref.startRune && runes.slice(ref.startRune, ref.endRune).join("") === ref.quote) {
      ranges.push({start:runes.slice(0, ref.startRune).join("").length, end:runes.slice(0, ref.endRune).join("").length});
      continue;
    }
    const positions = []; let offset = 0, at;
    while (ref.quote && (at = reviewNarration.indexOf(ref.quote, offset)) !== -1) { positions.push(at); offset = at + ref.quote.length; }
    const occurrence = Number(ref.occurrence);
    // A repeated quote without a saved occurrence is ambiguous; never guess its location.
    const start = positions.length === 1 ? positions[0] : Number.isInteger(occurrence) && occurrence > 0 ? positions[occurrence - 1] : undefined;
    if (start !== undefined) ranges.push({start, end:start + ref.quote.length});
  }
  ranges.sort((a,b) => a.start-b.start);
  let cursor = 0;
  for (const range of ranges) {
    if (range.start < cursor) continue;
    paragraph.append(document.createTextNode(reviewNarration.slice(cursor, range.start)), el("mark", reviewNarration.slice(range.start, range.end)));
    cursor = range.end;
  }
  paragraph.append(document.createTextNode(reviewNarration ? reviewNarration.slice(cursor) : missing));
  $("#review-text").replaceChildren(paragraph);
  const mark = paragraph.querySelector("mark");
  if (mark && typeof mark.scrollIntoView === "function") mark.scrollIntoView({block:"nearest", behavior:"smooth"});
}

function appendReviewReference(selection) {
  if (!project || !reviewData || reviewData.projectId !== project.id) throw Error("请先载入当前项目的作品");
  if (narrationDirty || productionDirty || dirty || editDSLDirty) throw Error("正文或剪辑有未保存修改，请先保存再添加定位引用");
  const historical = !!reviewData.renderId;
  if (!historical && reviewData.revision !== project.revision) throw Error("工程已更新，请刷新作品后再选择片段");
  if (!historical && reviewData.current === false) throw Error("当前工程与正文尚未核对，请先通过对话更新工程再添加定位引用");
  const lines = [`【${historical ? "历史作品审阅" : "当前工程审阅"}】项目 ${project.id}；修订 ${reviewData.revision}；后端 ${reviewData.backend || "未知"}`];
  if (reviewData.renderId) lines.push("成片 ID：" + reviewData.renderId);
  if (reviewData.sourceHash) lines.push("正文指纹：" + reviewData.sourceHash);
  if (selection.clip) {
    lines.push(`片段 ID：${selection.clip.id || "未提供"}；轨道：${selection.track.id}；时间：${reviewSeconds(selection.clip.start)}–${reviewSeconds(selection.clip.end)}`);
    if (selection.clip.assetId) lines.push("素材 ID：" + selection.clip.assetId);
    for (const ref of selection.clip.textRefs || []) if (ref.quote) lines.push("关联原文：" + ref.quote);
  } else if (selection.quote) lines.push("所选原文：" + selection.quote);
  lines.push(historical ? "这是历史版本的定位信息。请先核对当前工程，再按我的后续要求修改；不要覆盖较新的修改。" : "请先核对正文指纹和当前修订，再按我的后续要求修改。保持其余内容。");
  const composer = $("#prompt"); composer.value += (composer.value.trim() ? "\n\n" : "") + lines.join("\n");
  $("#chat").hidden = false; $(".layout").classList.remove("no-chat"); composer.focus(); composer.setSelectionRange(composer.value.length, composer.value.length);
  $("#review-status").textContent = "定位信息已加入对话草稿。补充修改要求后发送，尚未执行任何修改。";
}

function applyReviewZoom() {
  $("#review-timeline").style.minWidth = (Number($("#review-zoom").value) * 100) + "%";
}

function drawHyperframesProject(data) {
  if (!project || data.projectId !== project.id) throw Error("动画工程不属于当前项目");
  const panel = $("#hyperframes-project"), files = $("#hyperframes-files"); files.replaceChildren();
  panel.hidden = project.workflow?.editingBackend !== "hyperframes" && !data.document;
  const doc = data.document;
  if (!doc) { $("#hyperframes-summary").textContent = "尚未保存 HyperFrames 工程。由 Codex 通过制作工具编写动画和素材引用。"; return; }
  const canvas = doc.canvas || {};
  $("#hyperframes-summary").textContent = `修订 ${data.revision} · ${data.current === false ? "需重新核对正文" : "已保存"} · ${canvas.width || "?"} × ${canvas.height || "?"} · ${canvas.fps || "?"} fps · ${canvas.duration || "?"} 秒 · 入口 ${doc.entrypoint || "未提供"}`;
  for (const reason of data.staleReasons || []) files.append(el("p", reason, "review-warning small"));
  for (const file of doc.files || []) {
    const details = el("details"), pre = el("pre", file.content || "");
    details.append(el("summary", file.path || "源文件"), pre); files.append(details);
  }
  if ((data.assets || []).length) {
    const details = el("details"); details.append(el("summary", "素材引用"));
    for (const asset of data.assets) details.append(el("p", `${asset.assetId} → ${asset.path}`, "small")); files.append(details);
  }
}

async function loadReviewTimeline(force = false) {
  if (!project || reviewLoading) return;
  drawReviewVersions();
  const pid = project.id, gen = epoch, renderId = $("#review-version").value;
  const key = JSON.stringify([pid, project.revision, project.renders, renderId]);
  if (!force && key === reviewKey) return;
  const request = ++reviewRequest; reviewLoading = true;
  try {
    const data = await api("/api/projects/" + pid + "/review-timeline" + (renderId ? "?renderId=" + encodeURIComponent(renderId) : ""));
    if (gen !== epoch || request !== reviewRequest || project?.id !== pid || $("#review-version").value !== renderId) return;
    if (renderId && data.renderId !== renderId) throw Error("返回的作品版本与所选版本不一致，请刷新后重试");
    drawReviewTimeline(data); reviewKey = key;
    if (!renderId && (project.workflow?.editingBackend === "hyperframes" || project.hyperframes)) {
      try {
        const hyperframes = await api("/api/projects/" + pid + "/hyperframes");
        if (gen === epoch && request === reviewRequest && project?.id === pid) drawHyperframesProject(hyperframes);
      } catch (error) {
        if (gen === epoch && request === reviewRequest) { $("#hyperframes-project").hidden = false; $("#hyperframes-summary").textContent = "工程暂不可用：" + error.message; }
      }
    } else $("#hyperframes-project").hidden = true;
  } catch (error) {
    if (gen === epoch && request === reviewRequest) { reviewData = null; $("#review-status").textContent = "作品审阅暂不可用：" + error.message; }
    throw error;
  } finally { if (request === reviewRequest) reviewLoading = false; }
}

function initReviewTimeline() {
  $("#review-refresh").onclick = action(() => loadReviewTimeline(true));
  $("#review-version").onchange = action(async () => { reviewKey = ""; reviewRequest++; reviewLoading = false; await loadReviewTimeline(true); });
  $("#review-zoom").oninput = applyReviewZoom;
  $("#review-seek").oninput = () => seekReviewPosition($("#review-seek").value);
  $("#review-player").addEventListener("timeupdate", () => {
    const render = (project?.renders || []).find(item => item.id === reviewData?.renderId);
    updateReviewPosition($("#review-player").currentTime + (Number(render?.range?.start) || 0));
  });
  $("#review-text").addEventListener("mouseup", () => {
    const selection = window.getSelection(), container = $("#review-text");
    reviewQuote = selection && container.contains(selection.anchorNode) && container.contains(selection.focusNode) ? selection.toString().trim() : "";
    $("#review-select-text").disabled = !reviewQuote;
  });
  $("#review-select-text").onclick = action(() => { if (reviewQuote) appendReviewReference({quote:reviewQuote}); });
}
