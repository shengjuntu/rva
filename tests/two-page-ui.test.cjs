'use strict';

const {test}=require('node:test');
const assert=require('node:assert/strict');
const fs=require('node:fs');
const vm=require('node:vm');
const path=require('node:path');
const {JSDOM}=require('jsdom');
const root=path.resolve(__dirname,'..'),pid='a'.repeat(24),old='b'.repeat(24),fresh='c'.repeat(24);
const json=value=>new Response(JSON.stringify(value),{headers:{'Content-Type':'application/json'}});

function fixture(){
 const dom=new JSDOM(fs.readFileSync(path.join(root,'web/index.html'),'utf8'),{url:'http://localhost:3220/',runScripts:'outside-only'}),w=dom.window,context=dom.getInternalVMContext();
 w.HTMLMediaElement.prototype.pause=function(){};w.HTMLMediaElement.prototype.load=function(){};w.setInterval=()=>0;
 w.FormData=FormData;
 const state={project:{id:pid,name:'本周新闻',revision:1,narrationText:'旧正文',workflow:{editingBackend:'hyperframes',skills:['video-narration'],instructions:''},sessionId:'session',toolsVersion:'0.28.0',assets:[],renders:[{id:old,state:'completed',revision:1,duration:8,created:'2026-10-28T00:00:00Z'}]},view:{projectId:pid,requestId:'d'.repeat(24),state:'running',summary:'正在制作本次视频',updates:[]},calls:[],projects:[]};
 state.projects=[state.project];
 w.fetch=async(route,options={})=>{
  const url=new URL(route,w.location.href),method=options.method||'GET',body=options.body&&!(options.body instanceof FormData)?JSON.parse(options.body):undefined;
  state.calls.push({path:url.pathname,method,body});
  if(url.pathname==='/api/projects')return json(state.projects);
  if(url.pathname==='/api/projects/'+pid){if(method==='GET')return json(state.project);}
  if(url.pathname==='/api/projects/'+pid+'/production-state')return json(state.view);
  if(url.pathname==='/api/projects/'+pid+'/production-board')return json({projectId:pid,stages:[]});
  if(url.pathname==='/api/projects/'+pid+'/jobs')return json({jobs:[]});
  if(url.pathname==='/api/projects/'+pid+'/agent')return json({status:'idle'});
  if(url.pathname==='/api/projects/'+pid+'/events'||url.pathname==='/api/projects/'+pid+'/approvals')return json([]);
  if(url.pathname==='/api/projects/'+pid+'/draft/instruction')return json({text:'基于完整正文制作初稿'});
  if(url.pathname==='/api/projects/'+pid+'/narration'){state.project={...state.project,narrationText:body.text,revision:state.project.revision+1};return json(state.project);}
  if(url.pathname==='/api/projects/'+pid+'/workflow'){state.project={...state.project,workflow:body.workflow,revision:state.project.revision+1};return json(state.project);}
  if(url.pathname==='/api/projects/'+pid+'/messages')return json({requestId:body.requestId,state:'accepted'});
  if(url.pathname==='/api/projects/'+pid+'/review-timeline')return json({projectId:pid,renderId:url.searchParams.get('renderId'),current:true,fps:30,tracks:[{kind:'visual',label:'画面',clips:[{id:'opening',label:'开头',start:1.5,end:3.5}]}]});
  if(url.pathname==='/api/runtime-profiles')return json({defaultProfile:'',items:[]});
  return json({});
 };
 const source=fs.readFileSync(path.join(root,'web/app.js'),'utf8'),boot=source.lastIndexOf('\nboot().catch(');assert.ok(boot>0);
 vm.runInContext(source.slice(0,boot),context);
 vm.runInContext('showApp()',context);
 const run=code=>vm.runInContext(code,context),$=selector=>w.document.querySelector(selector);
 return{w,dom,state,run,$,close:()=>dom.window.close()};
}

test('one click saves the current text before one draft submission and the make page has no chat',async()=>{
 const f=fixture();try{
  f.state.view={projectId:pid,state:'idle'};
  await f.run(`openProject('${pid}')`);
  f.$('#narration-text').value='本次完整正文';f.$('#narration-text').dispatchEvent(new f.w.Event('input'));
  await f.run('startProduction()');
  const calls=f.state.calls,submit=calls.filter(row=>row.path.endsWith('/messages'));
  assert.equal(submit.length,1);assert.equal(submit[0].body.purpose,'draft');
  assert.ok(calls.findIndex(row=>row.path.endsWith('/narration'))<calls.findIndex(row=>row.path.endsWith('/messages')));
  assert.equal(f.state.project.narrationText,'本次完整正文');
  assert.equal(f.$('#page-make').hidden,false);assert.equal(f.$('#page-refine').hidden,true);
  assert.equal(f.$('#chat').closest('#page-make'),null);
  assert.equal(f.$('#draft-start').textContent,'开始制作');
 }finally{f.close();}
});

test('a running request hides the old movie; only a matching completed full render becomes this make-page output',async()=>{
 const f=fixture();try{
  await f.run(`openProject('${pid}')`);await f.run('loadProductionState(true)');
  assert.equal(f.$('#production-input').hidden,true);assert.equal(f.$('#production-progress').hidden,false);
  assert.equal(f.$('#production-progress').hasAttribute('value'),false);
  assert.equal(f.$('#outputs').hidden,true);assert.equal(f.$('#output-version-field').hidden,true);
  f.state.view={projectId:pid,requestId:'d'.repeat(24),state:'completed',renderId:fresh,summary:'本次已完成'};
  await f.run('loadProductionState(true)');assert.equal(f.$('#output-player').hidden,true,'render metadata has not arrived; no old movie may leak');
  f.state.project.renders.push({id:fresh,state:'completed',revision:2,duration:9,created:'2026-10-28T01:00:00Z',productionRequestId:'d'.repeat(24)});
  await new Promise(resolve=>setTimeout(resolve,0));await f.run('poll()');await f.run('loadProductionState(true)');
  assert.equal(f.$('#outputs').hidden,false);assert.match(f.$('#output-player').src,new RegExp(fresh),f.run('JSON.stringify({renders:project.renders,choice:outputChoice,selected:$("#output-version").value,state:productionState})'));
  assert.equal(f.$('#go-refine').hidden,false);
  f.state.view={projectId:pid,requestId:'e'.repeat(24),state:'failed',summary:'本次失败'};
  await f.run('loadProductionState(true)');assert.equal(f.$('#outputs').hidden,true);assert.equal(f.$('#production-retry').hidden,false);
 }finally{f.close();}
});

test('project switcher shows five recent projects but searches the full unarchived list',async()=>{
 const f=fixture();try{
  f.state.projects=Array.from({length:8},(_,index)=>({id:String(index+1).padStart(24,'0'),name:'项目 '+index,lastOpened:new Date(Date.UTC(2026,9,index+1)).toISOString(),archived:index===7,statusLabel:'已出片'}));
  await f.run('listProjects()');assert.equal(f.$('#projects').children.length,5);assert.equal(f.$('#project-all-list').children.length,2);assert.equal(f.$('#project-archive-list').children.length,1);
  f.$('#project-search').value='项目 0';f.$('#project-search').dispatchEvent(new f.w.Event('input'));
  assert.equal(f.$('#projects').children.length,1);assert.match(f.$('#projects').textContent,/项目 0/);
  assert.equal(f.$('#project-all').hidden,true);
 }finally{f.close();}
});

test('refine is a separate URL view; version-bound time and clip are sent with an adjustment request',async()=>{
 const f=fixture();try{
  f.state.view={projectId:pid,state:'idle'};await f.run(`openProject('${pid}')`);
  f.run(`setPage('refine')`);assert.equal(f.$('#page-make').hidden,true);assert.equal(f.$('#page-refine').hidden,false);
  assert.equal(new URL(f.w.location.href).searchParams.get('page'),'refine');
  await f.run(`loadReviewTimeline('${old}')`);
  f.$('#review-clips').value='visual:opening';f.$('#review-clips').dispatchEvent(new f.w.Event('change'));
  f.$('#output-player').currentTime=1.5;f.run('markReviewLocation()');f.$('#prompt').value='开头画面请改成地图';
  await f.run('submitMessage({preventDefault(){}})');
  const sent=f.state.calls.filter(row=>row.path.endsWith('/messages')).at(-1).body;
  assert.equal(sent.purpose,'adjustment');assert.match(sent.text,/开头画面请改成地图/);assert.match(sent.text,new RegExp(old));assert.match(sent.text,/片段 visual:opening/);
  assert.equal(f.$('#agent-stop').closest('#page-refine')!==null,true);
 }finally{f.close();}
});
