# HyperFrames authoring contract

## Save and render

Use video_hyperframes_get(projectId) to read the complete saved document, revision, sourceHash, documentHash, state and staleReasons. State ready means source/references are structurally current; it does not mean browser check, actual render or human review passed.

Use video_hyperframes_update(projectId,revision,document) with the full document:

```json
{
  "schemaVersion": 1,
  "entrypoint": "index.html",
  "canvas": {"width": 1080, "height": 1920, "fps": 30, "duration": 3},
  "hasAudio": false,
  "files": [{"path": "index.html", "content": "<complete original HTML>"}],
  "assets": [],
  "textSourceHash": "<current board.sourceHash>",
  "timeline": [{"id": "opening", "track": "visual", "kind": "animation", "label": "Opening", "start": 0, "duration": 3, "textRefs": [{"quote": "<exact saved narration>"}]}]
}
```

Do not submit placeholders. Bind actual asset IDs as assets=[{assetId:"real_id",path:"media/picture.png"}]; source then references media/picture.png. The App records actual fingerprints. Retain returned fingerprints on unchanged bindings; if bytes changed, inspect them and deliberately rebuild the relevant binding. Avoid duplicate/case-conflicting paths, parent traversal, absolute paths, symlinks or private filesystem references. runtime/ and output/manifest paths are reserved for the service.

Limits: index.html entrypoint; 1–128 UTF-8 source files, at most 512 KiB each and 1 MiB combined; up to 120 material bindings and 1000 review clips. Material files are at most 256 MiB each and 1 GiB combined. Canvas is 16–4096 pixels per side, fps 1–60 and duration greater than zero through 600 seconds; use even dimensions for H.264. Every review clip must fit the declared canvas duration. kind is image/video/animation/audio/voice/subtitle/music. Give stable valid IDs and tracks, optional label, actual start/duration, optional bound assetId and exact textRefs. Review metadata has no implicit cuts, fades or audio semantics.

Call video_hyperframes_check(projectId,revision) after saving. Read its actual status/log and rendered=false. Start a full video_job_start(type="render",options={"kind":"draft"}) with current revision and fresh requestId; poll actual job state. A successful check is not a render. Current authored HyperFrames rejects range renders. Keep any separate local experiments outside archived App outputs.

## Runtime conventions

Use the installed HyperFrames 0.8.87 conventions: a composition element with data-composition-id, data-start, data-duration, data-width, data-height and data-fps; timed visible/media elements with class clip and data-start/data-duration; a paused GSAP timeline registered under window.__timelines[compositionId]. Keep duration/size/fps equal to the saved canvas. All visual and audio implementation belongs in source, not optional document.timeline metadata.

The App freezes runtime/gsap.min.js and runtime/font.css with the configured font. Include those local paths and font-family:VideoFont. Bind narration, music, pictures and clips as project assets; never fetch a font, script or authenticated URL at render time. The service installs no new package during check/render. HyperFrames work is executable browser code, so keep it scoped to the artwork and selected local assets.

Original minimal silent animation (replace text/style and extend creatively):

```html
<!doctype html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<link rel="stylesheet" href="runtime/font.css">
<script src="runtime/gsap.min.js"></script>
<style>
* { box-sizing: border-box; }
body { margin: 0; }
#news { position: relative; width: 1080px; height: 1920px;
  overflow: hidden; background: #101820; color: #fff; font-family: VideoFont,sans-serif; }
.card { position: absolute; inset: 180px 80px; padding: 60px; background: #203746; }
h1 { font-size: 84px; line-height: 1.25; }
.rule { width: 100%; height: 12px; background: #61dec2; transform-origin: left; }
</style>
</head>
<body>
<main id="news" data-composition-id="news" data-start="0" data-duration="3"
      data-width="1080" data-height="1920" data-fps="30">
  <section class="clip card" data-start="0" data-duration="3" data-track-index="1">
    <h1 id="headline">新闻标题</h1><div class="rule"></div>
  </section>
</main>
<script>
const timeline = gsap.timeline({paused: true});
timeline.from('#headline', {y: 60, opacity: 0, duration: 0.7}, 0.15);
timeline.from('.rule', {scaleX: 0, duration: 0.8}, 0.45);
window.__timelines = window.__timelines || {};
window.__timelines.news = timeline;
</script>
</body>
</html>
```

This sample demonstrates animation mechanics, not a narration-complete draft. Set the real duration from audio/timing and implement all required pictures, captions and audio before full production. hasAudio must truthfully match the produced output; setting it true does not synthesize, attach or mix a voice track. A timeline clip alone does not create sound.

For a real narration asset, bind its actual assetId to media/voice.wav and add the timed audio element inside the composition. This 2-second form has been exercised against HyperFrames 0.8.87; replace duration/end with actual materialized timing, align the canvas/root duration, and set hasAudio=true:

```html
<audio id="voice" src="media/voice.wav" data-start="0" data-duration="2"
       data-end="2" data-track-index="4" data-volume="1"></audio>
```

Set source/generated video muted when it serves only as visual material. Implement timed subtitles as real visible clip elements with data-start/data-duration; their text and timing must match the actual voice cues. Adding subtitle/voice/music review entries alone has no rendering effect. Keep voice intelligible and any music subordinate; verify actual output sound instead of assuming volume declarations prove the mix.

## Source and review revisions

Preserve unrelated files, bindings, stable IDs and textRefs when changing an animation. Save a complete revision-checked document rather than overwriting project.json. Source changes produce new documentHash and a new render; old renders keep their frozen source, material and dependency hashes.

Use video_review_timeline_get(projectId,renderId?) for current or historical declaration and check current/sourceHash/output identity. Use video_edit_locate exact quotes for mapped objects. Timeline positions are declared, not actual audio or DOM extraction, and do not replace output inspection. Changed narration, material bytes or workflow can make earlier output stale. Revalidate what changed instead of rewriting hashes to hide it.
