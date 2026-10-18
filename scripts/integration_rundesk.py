#!/usr/bin/env python3
"""Real Video App / RunDesk HTTP, fixture Docker/Codex; never a live model test."""
import argparse, json, os, shutil, socket, subprocess, tempfile, threading, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.request import Request, urlopen
from urllib.error import HTTPError


def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1',0)); return s.getsockname()[1]


def http(method,url,body=None,token='',key=None):
    headers={'Content-Type':'application/json'}
    if token: headers['Authorization']='Bearer '+token
    if key: headers['Idempotency-Key']=key
    req=Request(url,data=json.dumps(body).encode() if body is not None else None,headers=headers,method=method)
    try:
        with urlopen(req,timeout=150) as r:return r.status,json.load(r)
    except HTTPError as e:return e.code,json.load(e)


def wait(url,token):
    for _ in range(100):
        try:
            if http('GET',url,token=token)[0]==200:return
        except OSError: pass
        time.sleep(.1)
    raise AssertionError('server startup timed out')


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--rundesk-root',required=True)
    parser.add_argument('--output',required=True)
    args=parser.parse_args()
    app=Path(__file__).resolve().parents[1]; rd=Path(args.rundesk_root).resolve()
    out=Path(args.output).resolve();out.mkdir(parents=True,exist_ok=True)
    checks=[];children=[]
    with tempfile.TemporaryDirectory(prefix='video-wire-') as tmp:
        root=Path(tmp);cli=root/'cli';cli.mkdir();engine=root/'docker';engine.mkdir()
        shutil.copyfile(rd/'scripts/fixtures/docker-engine.py',cli/'docker');(cli/'docker').chmod(0o755)
        rdurl='http://127.0.0.1:'+str(port());appurl='http://127.0.0.1:'+str(port())
        admin='integration-admin-only-01234567890';token='integration-app-only-01234567890'
        env={**os.environ,'PATH':str(cli)+os.pathsep+os.environ['PATH'],'DOCKER_CONFIG':str(engine),'RUNDESK_TOKEN':admin}
        processes=[]
        def launch(binary,arguments,extra,name):
            proc=subprocess.Popen([str(binary),*arguments],env={**env,**extra},stdout=(out/name).open('a'),stderr=subprocess.STDOUT)
            children.append(proc);return proc
        launch(rd/'bin/rundesk',['--demo','--data',str(root/'rundesk'),'--listen',rdurl.split('//')[1]],{},'rundesk.log')
        wait(rdurl+'/api/v1/meta',admin)
        def rcall(method,path,body=None):
            status,value=http(method,rdurl+'/api/v1'+path,body,admin)
            assert status<300,(path,status,value)
            return value
        class Proxy(BaseHTTPRequestHandler):
            def log_message(self,*a):pass
            def forward(self):
                body=json.loads(self.rfile.read(int(self.headers['Content-Length']))) if self.headers.get('Content-Length') else None
                status,value=http(self.command,rdurl+self.path,body,admin,self.headers.get("Idempotency-Key"))
                if self.path.endswith('/runtime/prepare') and status==200:
                    # Demo Codex cannot discover this MCP. Exercise its real HTTP
                    # callback here and substitute ONLY the simulated discovery list.
                    binding=rcall('GET','/applications/rundesk-video-app/runtime')
                    config=rcall('GET','/workspaces/'+binding['workspaceId']+'/mcp?instanceId='+binding['instanceId'])
                    callback=config['effectiveServers']['video_project']['url']
                    assert callback==appurl+'/api/mcp',callback
                    status2,rpc=http('POST',callback,{'jsonrpc':'2.0','id':1,'method':'tools/list'},token)
                    assert status2==200
                    value['servers']={'video_project':[tool['name'] for tool in rpc['result']['tools']]}
                raw=json.dumps(value).encode();self.send_response(status);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(raw)));self.end_headers();self.wfile.write(raw)
            do_GET=do_POST=do_PUT=do_PATCH=forward
        proxy=ThreadingHTTPServer(('127.0.0.1',0),Proxy);threading.Thread(target=proxy.serve_forever,daemon=True).start()
        proxyurl='http://127.0.0.1:'+str(proxy.server_port)
        def video():
            proc=launch(app/'bin/video-app',['--data',str(root/'video'),'--listen',appurl.split('//')[1],'--rundesk',proxyurl],{'VIDEO_APP_TOKEN':token,'VIDEO_APP_PUBLIC_URL':appurl,'VIDEO_APP_MCP_URL':appurl+'/api/mcp'},'video.log')
            wait(appurl+'/api/health',token);return proc
        def call(method,path,body=None):
            status,value=http(method,appurl+path,body,token);assert status<300,(path,status,value);return value
        try:
            running=video()
            assert http('GET',appurl+'/api/health')[0]==401
            p=call('POST','/api/projects',{'name':'Docker API acceptance'});route='/api/projects/'+p['id']
            p=call('POST',route+'/connect',{});old=p['sessionId'];iid=json.loads((root/'video/config.json').read_text())['instanceId']
            assert call('POST',route+'/connect',{})['sessionId']==old
            checks.append('idempotent_application_registration_and_session')
            instance=next(i for i in rcall('GET','/instances') if i['id']==iid)
            rcall('PUT','/instances/'+iid+'/execution',{'revision':instance['revision'],'spec':{'mode':'docker','image':'video:fixture','network':'bridge'}})
            status,value=http('POST',appurl+route+'/messages',{'text':'test','requestId':'before-reconnect'},token)
            assert status==409 and '运行方式' in value['error'],value
            p=call('POST',route+'/connect',{});assert p['sessionId']!=old and old in p['previousSessionIds']
            checks.append('empty_local_session_switches_to_docker_without_overwriting_history')
            # Runtime configs are independent. Emulate the documented one-time
            # administrator setup, not an application overwriting user config.
            binding=rcall('GET','/applications/rundesk-video-app/runtime')
            cp='/workspaces/'+binding['workspaceId']+'/mcp?instanceId='+iid
            saved=rcall('GET',cp)
            rcall('PUT','/workspaces/'+binding['workspaceId']+'/mcp/video_project?instanceId='+iid,{'version':saved['version'],'config':{'url':appurl+'/api/mcp','http_headers':{'Authorization':'Bearer '+token},'enabled':True}})
            ready=call('POST','/api/runtime/prepare',{});assert ready['probed'] and ready['accountReady'] and len(ready['servers']['video_project'])==10
            call('POST','/api/runtime/prepare',{})
            checks.append('docker_prepare_reuses_environment_and_real_mcp_http_callback')
            rpc=call('POST','/api/mcp',{'jsonrpc':'2.0','id':2,'method':'tools/call','params':{'name':'video_project_get','arguments':{'projectId':p['id']}}})
            assert not rpc['result'].get('isError'),rpc
            checks.append('business_mcp_project_read')
            message={'text':'协议测试：只验证运行链路','requestId':'fixture-message-001'}
            response=call('POST',route+'/messages',message);assert response['runId']
            assert call('POST',route+'/messages',message)['runId']==response['runId']
            state=rcall('GET','/sessions/'+p['sessionId']);assert state['executionMode']=='docker' and state['environmentId']==ready['environment']['id']
            checks.append('new_message_preflight_and_duplicate_request_reuse')
            call('POST',route+'/agent/stop',{'expectedRunId':response['runId']})
            for _ in range(100):
                if rcall('GET','/sessions/'+p['sessionId'])['status'] not in ['running','starting','stopping','waiting']:break
                time.sleep(.05)
            sid=p['sessionId'];newpath=route+'/connect?new=1&expectedSessionId='+sid
            p=call('POST',newpath,{});assert p['sessionId']!=sid and sid in p['previousSessionIds']
            assert call('POST',newpath,{})['sessionId']==p['sessionId']
            checks.append('explicit_new_conversation_is_fenced_and_retry_safe')
            running.kill();running.wait(timeout=10);video()
            loaded=call('GET',route);assert loaded['sessionId']==p['sessionId'] and loaded['workspaceId']==p['workspaceId']
            checks.append('sigkill_restart_releases_data_lock_and_preserves_state')
            calls=[json.loads(line) for line in (engine/'calls.jsonl').read_text().splitlines()]
            assert sum(c[:2]==['container','create'] for c in calls)==1
            assert sum(c[:2]==['container','start'] for c in calls)==1
            assert not any(c[0] in ['build','pull'] for c in calls)
            checks.append('one_container_create_start_and_no_build_on_registration_or_message')
            report={'passed':len(checks),'checks':checks,'mode':'real RunDesk and Video App HTTP; Docker CLI/Codex discovery fixtures','realDockerTested':False,'modelTested':False,'renderTested':False}
            (out/'integration.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n');print(json.dumps(report,ensure_ascii=False))
        finally:
            proxy.shutdown()
            for child in reversed(children):
                if child.poll() is None:child.terminate()
                try:child.wait(timeout=10)
                except subprocess.TimeoutExpired:child.kill();child.wait()

if __name__=='__main__':main()
