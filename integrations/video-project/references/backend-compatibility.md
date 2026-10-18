# Existing editing backends

Read this only when the project explicitly selects native or video_use. Do not silently switch backends or write two competing authoritative edits. New HyperFrames production does not need either path.

## Native compatibility backend

Read video_edit_get; use video_edit_validate then video_edit_update(projectId,revision,dsl). New work uses DSL, not legacy paragraph-shot projection. Active DSL ignores legacy shots/audio/subtitles. Add materialized narration explicitly on an audio track.

DSL v1 supports canvas, tracks, optional anchors and clips:

- Canvas: even width/height 32..3840 within 4K pixel count, fps 1..60, background="#RRGGBB", optional duration<=600. Default 1080x1920, 30 fps.
- At most 20 tracks(kind="video"/"audio"/"text"), 240 clips, 300 anchors. Later visual tracks overlay earlier ones; same-track overlaps stack by declaration order. Audio clips mix; source video sound is muted.
- IDs start with a letter, then letters/digits/underscore/dot/hyphen, at most 64 characters. Clips use exactly one start/after/anchor placement plus optional offset. Anchors use time OR clip/edge and offset. No dangling/cyclic references.
- Reference real assets. Images require duration and reject trim/speed. Audio/video use source={in,out,speed}, speed 0.5..2; supplied duration must agree. Render checks actual source duration.
- For new clips serving authored words, set document textSourceHash to the current board sourceHash and add optional textRefs=[{quote,occurrence?}]. Use exact original quotes; repeated/overlapping occurrences need a 1-based occurrence. A clip may reference several spans and a span may serve several clips. Keep background/legacy clips unreferenced when appropriate; never fabricate references or impose a scene schema. Stale references require validating the words again, not copying the new hash onto old claims.
- layout={x,y,width,height,fit:"cover"/"contain",opacity:0..1}. Text uses text,fontSize=8..240,color="#RRGGBB",layout and explicit line breaks. Overflow clips; no auto-wrap/text fit. Use a font supporting the script. Audio volume 0..2; fadeIn+fadeOut cannot exceed duration.
- No arbitrary transitions, keyframes, animated zoom, music looping or components. Import a real rendered asset for unsupported effects, or simplify without silently omitting words.

Start video_job_start(type="render",options={"kind":"draft"}) with current revision/fresh requestId; poll. Diagnostic range={start,end} is at most 60 seconds; output-relative time starts at zero. Continue to a full output: short preview or compile success is not a complete draft.

## video_use compatibility backend

After Stage 1 inputs are ready and required runtime diagnostics pass, call video_production_run_prepare(projectId,revision,requestId). It freezes/exports a snapshot and returns productionRunId, relativeRunRoot, manifestPath, sourceHash and inputHash. Read video_production_run_get(projectId,productionRunId) to resume. Use returned paths, never guessed mounts. Exports are scoped copies, not mounted App private data.

Write work/edl.json under that run root. The script-media adapter supports schemaVersion=1, canvas={width,height,fps}, clips=[{id,path,type:"image"/"video",start,duration,sourceIn?,fit:"cover"/"contain",assetId?,textRefs?}] and optional subtitles={fontName?,fontSize?,marginV?}. Paths must match exported manifest. Give new clips optional textRefs=[{quote,occurrence?}] with exact quotes from frozen manifest textPath; manifest sourceHash binds those original bytes. Use stable clip IDs, at most 32 references/clip and 1-based occurrence for repeated/overlapping matches. An EDL needs no top-level textSourceHash. Allow unreferenced background and legacy clips; do not invent links. This baseline has one visual track; cover the actual narration duration with no gaps/overlaps. Keep EDL authoritative, without mirroring it to native DSL. Native backend has separate overlay/music capabilities; do not claim parity or silently switch the selected backend.

Run the installed adapter with the returned root substituted:

    python3 /opt/rundesk-profile/tools/video_use_adapter.py render --manifest <relativeRunRoot>/input/manifest.json --edl <relativeRunRoot>/work/edl.json --out <relativeRunRoot>/artifacts/render-<unique>

Then call video_production_run_ingest(projectId,revision,productionRunId). Only ingest verifies/archives the completion and exposes a real render. Prepare success or a shell command alone is not a draft. Follow installed video-use resources where relevant; upstream footage-editing rules do not override authored script timing or continuous external narration. Missing adapter/shared directory is a concrete blocker.


For native local revisions use video_edit_patch_preview then video_edit_patch_apply with identical changes/base revision. Items use target="clip"/"track"/"anchor"/"canvas", id, op="add"/"update"/"delete", fields. Canvas id="canvas" is update-only. fields merges top-level; nested source/layout replace complete objects; null clears optional placement. IDs cannot change. Fix related references atomically. Full saves handle reordering. Undo/redo require current revision and matching branch.

For video_use changes prepare a new snapshot, reuse earlier EDL decisions with refreshed exported paths and preserved clip IDs/references, then render/ingest that new run. Never mutate archived run inputs or outputs. Revalidate quotes against new frozen narration instead of replacing a hash on stale references. Use video-build 1.4.0 with its optional video-use installation (adapter contract 2); default HyperFrames-only images intentionally do not provide this legacy dependency.
