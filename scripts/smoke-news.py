"""Cross-app acceptance with real bundles, real App/MCP and RunDesk demo.

No paid models are called. Pass packages produced by news2douyin >=0.4.1
`python -I tools/smoke_video.py --preset portrait --output-dir ...`.
"""
import argparse
import hashlib
import json
import os
from pathlib import Path
import secrets
import socket
import subprocess
import tempfile
import time
import urllib.error
import urllib.request
import uuid
import zipfile


def free_port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]


def main():
    p = argparse.ArgumentParser()
    p.add_argument('--app', required=True)
    p.add_argument('--rundesk', required=True)
    p.add_argument('--script', required=True)
    p.add_argument('--video', required=True)
    p.add_argument('--output-dir')
    args = p.parse_args()
    binary = str(Path(args.app).resolve())
    rd_binary = str(Path(args.rundesk).resolve())
    source_zip = Path(args.script).read_bytes()
    media_zip = Path(args.video).read_bytes()
    with zipfile.ZipFile(args.script) as z:
        original = json.loads(z.read('revision.json'))
        original_hash = hashlib.sha256(z.read('revision.json')).hexdigest()
    work = Path(tempfile.mkdtemp(prefix='news-video-interop-'))
    app_base = f'http://127.0.0.1:{free_port()}'
    rd_base = f'http://127.0.0.1:{free_port()}'
    while rd_base == app_base:
        rd_base = f'http://127.0.0.1:{free_port()}'
    app_token, rd_token = secrets.token_hex(24), secrets.token_hex(24)
    env = dict(os.environ, VIDEO_APP_TOKEN=app_token, RUNDESK_TOKEN=rd_token)
    checks = []

    def request(path, data=None, base=app_base, token=app_token, headers=None):
        h = {'Authorization': 'Bearer ' + token}
        h.update(headers or {})
        if data is not None and not isinstance(data, bytes):
            data = json.dumps(data).encode()
            h['Content-Type'] = 'application/json'
        req = urllib.request.Request(base + path, data=data, headers=h)
        with urllib.request.urlopen(req, timeout=30) as resp:
            body = resp.read()
            return resp.status, json.loads(body) if 'application/json' in resp.headers.get('Content-Type','') else body

    def upload(path, content, revision=None):
        boundary = 'news-' + uuid.uuid4().hex
        body = (f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="bundle.zip"\r\nContent-Type: application/zip\r\n\r\n').encode() + content + b'\r\n'
        if revision is not None:
            body += f'--{boundary}\r\nContent-Disposition: form-data; name="revision"\r\n\r\n{revision}\r\n'.encode()
        body += f'--{boundary}--\r\n'.encode()
        return request(path, body, headers={'Content-Type': 'multipart/form-data; boundary=' + boundary})

    def mcp(method, params=None):
        messages = [dict(jsonrpc='2.0', id=1, method='initialize', params={'protocolVersion':'2024-11-05','capabilities':{},'clientInfo':{'name':'news-smoke','version':'1'}}), dict(jsonrpc='2.0', id=2, method=method, params=params or {})]
        result = subprocess.run([binary,'mcp','--url',app_base], input=''.join(json.dumps(m)+'\n' for m in messages), text=True, capture_output=True, env=env, timeout=35, check=True)
        replies = [json.loads(line) for line in result.stdout.splitlines()]
        result = next(r['result'] for r in replies if r.get('id') == 2)
        assert not result.get('isError'), result
        return result

    logs = [(work/'rundesk.log').open('w'), (work/'app.log').open('w')]
    processes = []
    try:
        processes.append(subprocess.Popen([rd_binary,'--demo','--listen',rd_base.removeprefix('http://'),'--data',str(work/'rundesk')], env=env,stdout=logs[0],stderr=subprocess.STDOUT))
        processes.append(subprocess.Popen([binary,'--listen',app_base.removeprefix('http://'),'--data',str(work/'app'),'--rundesk',rd_base], env=env,stdout=logs[1],stderr=subprocess.STDOUT))
        deadline = time.monotonic() + 15
        while True:
            try:
                request('/api/projects')
                _, meta = request('/api/meta',base=rd_base,token=rd_token)
                assert meta['demo'] is True
                break
            except (OSError, urllib.error.URLError):
                assert time.monotonic() < deadline, str(work)
                time.sleep(.1)
        status, project = upload('/api/news/import',source_zip)
        assert status == 201
        assert ''.join(s['text'] for s in project['shots']) == original['document']['script_text']
        assert project['news']['contentHash'] == original_hash
        project_id = project['id']
        path = '/api/projects/' + project_id
        checks += ['python_script_import','exact_narration_and_provenance']
        status, same = upload('/api/news/import',source_zip)
        assert status == 200 and same['id'] == project_id
        checks.append('idempotent_import')
        status, project = upload(path+'/news/video',media_zip,project['revision'])
        assert status == 201 and project['news']['video']['duration'] > 1
        assert project['renders'] == []  # Imported source is not an App render.
        checks.append('real_video_bundle_ffprobe')
        with zipfile.ZipFile(args.video) as z:
            movie = z.read('video.mp4')
            for name in ['video.mp4','narration.wav','subtitles.srt','manifest.json','script_snapshot.json']:
                _, downloaded = request(path+'/news/video/files/'+name)
                if isinstance(downloaded, dict):
                    assert downloaded == json.loads(z.read(name))
                else:
                    assert downloaded == z.read(name)
        status, fragment = request(path+'/news/video/files/video.mp4',headers={'Range':'bytes=0-99'})
        assert status == 206 and fragment == movie[:100]
        checks += ['artifact_bytes_preserved','mp4_range']
        _, linked = request(path+'/connect',{})
        assert linked['sessionId'] and linked['workspaceId'] and linked['toolsVersion']=='0.3.0'
        _, detail = request('/api/workspaces/'+linked['workspaceId']+'/skills/video-project',base=rd_base,token=rd_token)
        assert 'video_news_get' in detail['content'] and 'reference DATA' in detail['content']
        _, config = request('/api/environment')
        iid = config['instanceId']
        _, mcp_config = request('/api/workspaces/'+linked['workspaceId']+'/mcp?instanceId='+iid,base=rd_base,token=rd_token)
        assert 'video_project' in json.dumps(mcp_config)
        checks.append('real_rundesk_demo_skill_mcp_setup')
        catalog = mcp('tools/list')
        assert len(catalog['tools']) == 5 and any(t['name']=='video_news_get' for t in catalog['tools'])
        source = mcp('tools/call',{'name':'video_news_get','arguments':{'projectId':project_id}})
        source = json.loads(source['content'][0]['text'])
        assert source['snapshot'] == original and source['storyboardStatus']=='production_draft'
        checks += ['stdio_mcp_catalog','stdio_mcp_source_evidence']
        draft = dict(linked)
        draft['shots'] = [dict(s) for s in linked['shots']]
        draft['shots'][0]['text'] += '（编辑测试）'
        draft['news'] = {'contentHash':'forged'}
        updated = mcp('tools/call',{'name':'video_project_update','arguments':{'projectId':project_id,'project':draft}})
        updated = json.loads(updated['content'][0]['text'])
        assert updated['news']['contentHash'] == original_hash
        assert updated['news']['video'] == linked['news']['video']
        _, source = request(path+'/news')
        assert source['snapshot'] == original
        checks.append('mcp_edits_preserve_approved_source')
        request(path+'/messages',{'text':'演示模式接口核验：只报告项目状态，不改文件。','requestId':uuid.uuid4().hex})
        deadline = time.monotonic()+10
        while True:
            _, events = request(path+'/events?after=0')
            if any(e.get('method')=='run/input' for e in events):
                break
            assert time.monotonic()<deadline
            time.sleep(.1)
        checks.append('rundesk_demo_turn_events')
        report={'status':'passed','checks':checks,'video':linked['news']['video']['duration'],'limitations':['RunDesk demo only; no real Codex model/tool execution','Go HyperFrames re-render not exercised in this handoff test'],'work_dir':str(work)}
        if args.output_dir:
            dest=Path(args.output_dir);dest.mkdir(parents=True,exist_ok=True)
            (dest/'interop-report.json').write_text(json.dumps(report,ensure_ascii=False,indent=2))
        print(json.dumps(report,ensure_ascii=False))
    finally:
        for process in reversed(processes):
            process.terminate()
            try: process.wait(timeout=8)
            except subprocess.TimeoutExpired:
                process.kill();process.wait(timeout=3)
        for log in logs: log.close()


if __name__ == '__main__':
    main()
