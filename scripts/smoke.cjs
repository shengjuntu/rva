const { spawn, execFileSync } = require("node:child_process");
const fs = require("node:fs");
const assert = require("node:assert/strict");
const { chromium } = require(process.env.PLAYWRIGHT_MODULE || "playwright");
const path = require("node:path"),
  os = require("node:os");
const root = fs.mkdtempSync(path.join(os.tmpdir(), "video-smoke-"));
const app = path.resolve(__dirname, ".."),
  renderer = process.env.VIDEO_RENDERER_DIR || path.join(app, "runtime");
const rdBinary = process.env.RUNDESK_BIN,
  chrome = process.env.HYPERFRAMES_BROWSER_PATH,
  font = process.env.VIDEO_FONT;
if (!rdBinary || !chrome || !font)
  throw new Error("Set RUNDESK_BIN, HYPERFRAMES_BROWSER_PATH, VIDEO_FONT");
const env = {
  ...process.env,
  HYPERFRAMES_NO_TELEMETRY: "1",
  DO_NOT_TRACK: "1",
  VIDEO_APP_TOKEN: "video-smoke-app-token-12345678",
  RUNDESK_TOKEN: "video-smoke-rundesk-token-12345",
  HYPERFRAMES_BROWSER_PATH: chrome,
};
console.log("Smoke artifacts:", root);
const runid = Date.now();
const rdData = root + "/video-app-smoke-rd-" + runid;
const appData = root + "/video-app-smoke-data-" + runid;
const children = [];
function start(exe, args) {
  const child = spawn(exe, args, { env, stdio: ["ignore", "pipe", "pipe"] });
  children.push(child);
  child.stdout.on("data", (x) => process.stdout.write(x));
  child.stderr.on("data", (x) => process.stdout.write(x));
  return child;
}
const delay = (ms) => new Promise((r) => setTimeout(r, ms));
async function req(path, body, method) {
  const r = await fetch("http://127.0.0.1:3322" + path, {
    method: method || (body ? "POST" : "GET"),
    headers: {
      Authorization: "Bearer " + env.VIDEO_APP_TOKEN,
      "Content-Type": "application/json",
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const d = await r.json();
  if (!r.ok) throw new Error(r.status + " " + JSON.stringify(d));
  return d;
}
(async () => {
  let browser;
  try {
    for (const [name, hz, duration] of [
      ["配音测试音.wav", 440, 2],
      ["音乐测试音.wav", 880, 0.8],
    ])
      execFileSync("ffmpeg", [
        "-v",
        "error",
        "-f",
        "lavfi",
        "-i",
        `sine=frequency=${hz}:sample_rate=48000:duration=${duration}`,
        path.join(root, name),
      ]);
    start(rdBinary, ["--demo", "--data", rdData, "--listen", "127.0.0.1:3321"]);
    start(app + "/bin/video-app", [
      "--data",
      appData,
      "--listen",
      "127.0.0.1:3322",
      "--rundesk",
      "http://127.0.0.1:3321",
      "--hyperframes",
      renderer + "/node_modules/hyperframes/bin/hyperframes.mjs",
      "--font",
      font,
      "--gsap",
      renderer + "/node_modules/gsap/dist/gsap.min.js",
    ]);
    for (let i = 0; i < 30; i++) {
      try {
        await req("/api/projects");
        break;
      } catch {
        await delay(200);
      }
    }
    let p = await req("/api/projects", { name: "从想法到视频" });
    const base = "/api/projects/" + p.id;
    p.brief = "介绍视频工作台：管理素材、组织分镜，让 Agent 参与创作。";
    p.shots = [
      {
        title: "从想法到视频",
        text: "项目、素材、分镜，在同一个工作台完成。",
        duration: 2,
        assetId: "",
      },
      {
        title: "让 Agent 参与创作",
        text: "RunDesk 管理运行，视频 App 管理作品。",
        duration: 2,
        assetId: "",
      },
    ];
    p = await req(base, p, "PUT");
    p = await req(base + "/connect", {});
    assert(p.sessionId);
    console.log("CONNECT OK", p.id);
    fs.writeFileSync(root + "/video-smoke-project.txt", p.id);
    let render;
    const mcp = spawn(
      app + "/bin/video-app",
      ["mcp", "--url", "http://127.0.0.1:3322"],
      { env },
    );
    children.push(mcp);
    let output = "";
    mcp.stdout.on("data", (x) => (output += x));
    mcp.stdin.end(
      JSON.stringify({
        jsonrpc: "2.0",
        id: 1,
        method: "tools/call",
        params: { name: "video_project_get", arguments: { projectId: p.id } },
      }) + "\n",
    );
    await new Promise((r) => mcp.on("exit", r));
    assert.equal(
      JSON.parse(JSON.parse(output).result.content[0].text).id,
      p.id,
    );
    console.log("MCP READ OK");
    browser = await chromium.launch({
      executablePath: env.HYPERFRAMES_BROWSER_PATH,
      args: ["--no-sandbox"],
    });
    const page = await browser.newPage({
      viewport: { width: 1500, height: 1050 },
    });
    const errors = [];
    page.on("pageerror", (e) => errors.push(e.message));
    await page.goto("http://127.0.0.1:3322");
    await page.locator("#token").fill(env.VIDEO_APP_TOKEN);
    await page.locator("#login-form button").click();
    await page
      .locator("#project-name")
      .filter({ hasText: "从想法到视频" })
      .waitFor();
    await page
      .locator("#upload")
      .setInputFiles([
        path.join(root, "配音测试音.wav"),
        path.join(root, "音乐测试音.wav"),
      ]);
    await page.waitForFunction(
      () => document.querySelectorAll("#assets .asset").length === 2,
    );
    await page
      .locator("#narration-asset")
      .selectOption({ label: "配音测试音.wav" });
    await page.locator("#narration-start").fill("0.5");
    await page.locator("#narration-sourceStart").fill("0.25");
    await page.locator("#narration-volume").fill("90");
    await page
      .locator("#music-asset")
      .selectOption({ label: "音乐测试音.wav" });
    await page.locator("#music-volume").fill("15");
    await page.locator("#music-loop").check();
    await page.locator("#save").click();
    await page.waitForFunction(() =>
      document.querySelector("#save-state").textContent.includes("已保存"),
    );
    p = await req(base);
    assert.equal(p.audio.narration.start, 0.5);
    assert.equal(p.audio.narration.sourceStart, 0.25);
    assert.equal(p.audio.music.volume, 0.15);
    await page.locator("#refresh-tools").click();
    await page.waitForFunction(() =>
      document.querySelector("#toast").textContent.includes("工具配置已更新"),
    );
    assert.equal((await req(base)).sessionId, p.sessionId);
    render = await req(base + "/render", {});
    assert.equal(render.hasAudio, true);
    console.log("AUDIO CONTROLS + TOOL REFRESH OK", render.id);
    await page.locator("#shot-title").fill("草稿不会丢失");
    const other = await req(base);
    other.brief += "（外部编辑）";
    await req(base, other, "PUT");
    await page.locator("#conflict").waitFor({ state: "visible" });
    assert.equal(
      await page.locator("#shot-title").inputValue(),
      "草稿不会丢失",
    );
    page.on("dialog", (d) => d.accept());
    await page.locator("#reload-project").click();
    await page.locator("#conflict").waitFor({ state: "hidden" });
    console.log("DRAFT CONFLICT OK");
    await page.locator("#prompt").fill("请展示轨迹并请求审批，测试协议。");
    await page.locator("#send").click();
    await page.locator("#conversation details").first().waitFor();
    await page.locator("#conversation details summary").first().click();
    await page.locator("#approvals .approval").waitFor();
    await page.locator("#prompt").fill("补充要求：保持两镜头。");
    await page.locator("#send").click();
    await page.waitForFunction(() =>
      document
        .querySelector("#send-state")
        .textContent.includes("补充指令已接收"),
    );
    assert(
      await page
        .locator("#conversation details")
        .first()
        .evaluate((n) => n.open),
    );
    console.log("STEER + STABLE DETAILS OK");
    await page.getByRole("button", { name: "允许本次", exact: true }).click();
    for (let i = 0; i < 150; i++) {
      const cur = await req(base);
      const job = cur.renders.find((x) => x.id === render.id);
      if (!["running", "stopping"].includes(job.state)) {
        console.log("RENDER RESULT", JSON.stringify(job));
        if (job.state !== "completed") {
          const log = fs.readFileSync(
            appData +
              "/projects/" +
              p.id +
              "/renders/" +
              job.id +
              "/render.log",
            "utf8",
          );
          console.log(log.slice(-18000));
          throw new Error("render failed");
        }
        break;
      }
      if (i === 149) throw new Error("render timeout");
      await delay(1000);
    }
    await page
      .getByRole("button", { name: "播放", exact: true })
      .first()
      .waitFor();
    await page
      .getByRole("button", { name: "播放", exact: true })
      .first()
      .click();
    await page.waitForFunction(
      () => document.querySelector("#player").readyState >= 2,
    );
    await page.locator("#player").evaluate((v) => {
      v.pause();
      v.currentTime = 1;
    });
    await delay(300);
    await page.locator("main").evaluate((n) => (n.scrollTop = 0));
    await page.locator("#conversation").evaluate((n) => (n.scrollTop = 0));
    await page.screenshot({
      path: root + "/video-app-preview.png",
      fullPage: true,
    });
    assert.deepEqual(errors, []);
    console.log("BROWSER + MP4 PLAYBACK OK");
    await page.locator("#sound-section").scrollIntoViewIfNeeded();
    await page.screenshot({
      path: root + "/video-app-audio-preview.png",
      fullPage: true,
    });
    const job = (await req(base)).renders.find((x) => x.id === render.id);
    fs.copyFileSync(
      appData + "/projects/" + p.id + "/renders/" + job.id + "/final.mp4",
      root + "/video-app-sample.mp4",
    );
    const media = JSON.parse(
      execFileSync(
        "ffprobe",
        [
          "-v",
          "error",
          "-show_streams",
          "-show_format",
          "-of",
          "json",
          root + "/video-app-sample.mp4",
        ],
        { encoding: "utf8" },
      ),
    );
    assert(
      media.streams.some(
        (s) => s.codec_type === "audio" && s.codec_name === "aac",
      ),
    );
    assert.equal(Number(media.format.duration), 4);
    console.log("REAL AAC MIX OK");
    const cancelJob = await req(base + "/render", {});
    await req(base + "/renders/" + cancelJob.id + "/stop", {});
    for (let i = 0; i < 30; i++) {
      const j = (await req(base)).renders.find((x) => x.id === cancelJob.id);
      if (j.state === "interrupted") {
        console.log("RENDER CANCEL OK");
        break;
      }
      if (i === 29) throw new Error("cancel timeout");
      await delay(200);
    }
    await page.setViewportSize({ width: 390, height: 844 });
    await page.waitForTimeout(100);
    assert(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth,
      ),
    );
    console.log("MOBILE LAYOUT OK");
  } finally {
    if (browser) await browser.close();
    for (const c of children) c.kill("SIGINT");
  }
})().catch((e) => {
  console.error(e);
  process.exitCode = 1;
});
