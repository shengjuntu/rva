# Shared-workspace production exchange, v0.21

The App exports a frozen input snapshot; RunDesk/Codex edits and renders in its
existing workspace; the App independently validates and privately archives the
result. This backend is selected with `workflow.editingBackend="video_use"`.
The native DSL backend remains available for existing projects.

## Deployment

Configure `VIDEO_EXCHANGE_ROOT` to the App's view of the same existing RunDesk
workspace mounted into the `video-build` environment. For example, mount that
one workspace into the App at `/exchange` and set the variable accordingly.
The Agent sees its normal workspace path. The App and Agent container paths do
not have to match: all exchange paths are relative to that workspace.

This does not request arbitrary extra mounts or grant the Agent access to the
App's private data, tokens, configuration or project database. Input snapshots
are copied, never hardlinked to original material. The workspace is an existing
shared trust boundary; subdirectories are logical project/run separation, not
an isolation boundary between different Agents sharing the same workspace.
The App requires read/write access and a compatible UID; it does not create a
host mount or verify that two container mounts refer to the same physical
volume. An absent/mismatched deployment produces a concrete preparation or
adapter-file error.

## Stage 1 → Stage 2: prepare

`video_production_run_prepare` / `POST /api/projects/{id}/production-runs/prepare`
accepts `{revision,requestId}`. The MCP wrapper also takes `projectId`.

Prerequisites are a valid RunDesk session, selected `video_use` backend, current
complete saved media alignment, and a completed independent narration TTS job
for the exact current authored text. `textcard` selections need an actual image
asset for this first adapter; a planned card cannot silently become an image.
Only selected image/video assets are exported. Missing prerequisites are a 409.

The receipt contains a 24-hex production run ID, narration `sourceHash`, combined
`inputHash`, metadata `inputBasisHash`, private snapshot digest, source-file
hashes and sizes, and relative paths. It never returns a host workspace path.
The same request ID and original request body return the original persistent
receipt even if the project has changed; changing the body under the same ID
is a 409. A new edit/render attempt uses a fresh request ID and production run.
Preparation increments project revision; re-read it before any subsequent write.

Paths below `outputs/<sessionId>/runs/<productionRunId>/`:

| Path | Owner and purpose |
| --- | --- |
| `input/manifest.json` | App-exported adapter input contract |
| `input/narration.txt` | Exact saved UTF-8 authored text |
| `input/alignment.json` | Saved text/media selections and reasons |
| `input/narration.wav` | Verified completed independent narration |
| `input/subtitles.srt` | TTS captions with measured synthesis timing |
| `input/speech-manifest.json` | Original verified TTS timing metadata |
| `input/media/<assetId>.<ext>` | Copied selected material |
| `work/edl.json` | Agent's authoritative editing document |
| `artifacts/<render-id>/...` | Adapter's real render and diagnostic outputs |
| `completion.json` | Atomically published result receipt |

`input/manifest.json` has `schemaVersion:1`, project/run IDs, `sourceHash`,
`inputHash`, `textPath`, `alignmentPath`, `timingPath`, and:

- `media:[{assetId,path,type,mimeType,sha256,size}]`, where type is image/video;
- `narration:{path,duration,jobId,sha256,size}`;
- `subtitles:{path,sha256,size}`.

Paths inside this manifest are relative to the run directory. `manifestPath`
in the preparation response is relative to the workspace. The `inputHash` is
SHA-256 of Go's canonical JSON encoding of `{Manifest,Files,Basis}` before
`Manifest.inputHash` is populated and before the manifest itself is added to
`Files`. The final serialized manifest digest is separately stored in the run
receipt. Consumers verify the declared file digests and echo the opaque input
hash; they do not reserialize JSON to guess the hash.

The basis includes narration, workflow, saved editorial alignment, selected
asset metadata/provenance, and completed current narration job metadata. It
excludes project revision, unrelated checkpoints and update timestamps. Actual
file digests are checked separately. A private original project snapshot is
saved at preparation so late results retain their true source snapshot.

## Stage 2: edit and render

The response gives this command template, executed from RunDesk's workspace:

```sh
python3 /opt/rundesk-profile/tools/video_use_adapter.py render \
  --manifest outputs/<sessionId>/runs/<productionRunId>/input/manifest.json \
  --edl outputs/<sessionId>/runs/<productionRunId>/work/edl.json \
  --out outputs/<sessionId>/runs/<productionRunId>/artifacts/<new-render-id>
```

EDL v1 uses `canvas:{width,height,fps}`, a continuous single visual track
`clips:[{id,assetId?,path,type,start,duration,sourceIn?,fit?}]`, and optional
subtitle styling. There is no native DSL conversion or double-written editing
state. This first adapter covers pictures and video under continuous external
narration, not arbitrary transitions, overlays, music mixing or still-image
animation. Use the native backend for its implemented multi-track features.
The vendored adapter validates source paths/digests, coverage and source trims,
renders using pinned upstream video-use helpers, adds narration/captions, and
publishes completion only after actual media checks and sample frames.

`completion.json` requires `schemaVersion:1`, `projectId`, `productionRunId`,
`sourceHash`, `inputHash`, `engine:"video_use"`, the pinned `upstreamCommit`,
and `artifacts`. Optional `video` diagnostics are accepted. The validation
report must identify the same project/run/source/input, bind the frozen EDL
and narration hashes, and contain passed technical checks. App independently
validates EDL paths, canvas, real source bounds and full narration coverage;
an empty or failed report is rejected. Each artifact is
`{id,kind,path,sha256,size,mimeType}`. Exactly one each of kinds `video`, `edit`
and `validation` is mandatory. Optional `probe` and `frame` records are accepted
up to 30 total artifacts. Every path must be below `artifacts/`, relative to
the run root, without traversal or symlinks. The video is H.264/AAC and must
include a full audio stream whose measured duration matches narration.

## Stage 2 → Stage 3: ingest and refine

`video_production_run_ingest` / `POST /api/projects/{id}/production-runs/{runId}/ingest`
takes `{revision}`. `video_production_run_get` / `GET` on that run returns the
persistent receipt, currentness and adapter command.

Ingestion verifies every frozen input digest, binds completion to the original
project/run/source/input hashes, validates artifact paths, and copies regular
bounded files through `os.Root` into private staging. It hashes the actual
copied bytes and probes that private video, so a shared-file change cannot
replace the checked video between probing and archiving. Inputs are rechecked
before commit. Limits are 2 GiB per media file, 4 GiB total, 4 MiB JSON and
8 MiB sampled images; narration/output duration is bounded to ten minutes.

The App archives `final.mp4`, the original private `snapshot.json`,
`edit-ir.json` (external EDL), `validation.json`, `render-proof.json`, captions
and optional evidence under its private `renders/<renderId>/`. It creates a
completed `Render` with `engine:"video_use"`. These are ordinary App-owned
artifacts after acceptance; subsequent deletion or mutation of the exchange
workspace cannot change them. Actual visual quality and factual suitability
still require Agent/human review; successful technical validation is not
human acceptance.

Commit checks revision again. Concurrent project changes cause 409 and retry
with a current revision. Changed content does not discard a late result:
it is archived with its original snapshot and reported stale. Current editing
authority is the latest **prepared** completed run for the current basis,
independent of result arrival order; late older outputs cannot replace newer
accepted edits. A merely prepared newer attempt does not invalidate the prior
completed result. Repeated ingest returns the original render without copies
or duplicate project renders. To revise EDL, prepare a fresh run.

Final designation remains a separate explicit human action. Final verification
checks the current private source bytes, archived video/snapshot/EDL digests and
backend authority. It does not rely on a mutable shared completion receipt.

## Validation

`production_exchange_test.go` covers durable idempotency, changed request
bodies, copy isolation, frozen-input tampering, traversal/symlink escape,
invalid artifact paths, original-asset/archived-EDL tampering, late stale
results, and newer edit authority despite older late ingestion.

The offline real-render tests require `VIDEO_USE_TEST_ADAPTER` pointing to the
bundled adapter and `VIDEO_USE_TEST_ROOT` to the pinned upstream checkout. They
exercise actual video-use/FFmpeg rendering, App ingestion, private archives,
final designation and proof checks. They do not test a live RunDesk/Codex model
session, Docker mounts or IndexTTS audio quality.

Shared input directories are 0700 and files are 0600. Video App and the RunDesk execution container must map to the same host UID; matching group alone is insufficient. For regular Docker match VIDEO_UID/VIDEO_GID to the RunDesk service. For rootless Docker verify the effective host ownership mapping.
