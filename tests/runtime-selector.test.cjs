'use strict';
const {test}=require('node:test');
const assert=require('node:assert/strict');
const {JSDOM}=require('jsdom');
const fs=require('node:fs'),path=require('node:path'),vm=require('node:vm');
const root=path.resolve(__dirname,'..');
const createKey='video-project-create-v2',legacyKey='video-default-project-v1';
const json=(value,status=200)=>new Response(JSON.stringify(value),{status,headers:{'Content-Type':'application/json'}});
const copy=value=>JSON.parse(JSON.stringify(value));
function catalog(){return{defaultProfile:'news',rundeskURL:'http://127.0.0.1:3210',warnings:[],items:[
 {id:'',name:'原默认环境',description:'兼容原项目',enabled:true,isDefault:false,state:'stopped',selectable:true},
 {id:'news',name:'新闻视频',description:'新闻素材与配音制作',enabled:true,isDefault:true,state:'running',selectable:true},
 {id:'animation',name:'动画环境',description:'HTML 动画与图表',enabled:true,isDefault:false,state:'not_started',selectable:true},
 {id:'retired',name:'旧环境',description:'已停用',enabled:false,isDefault:false,state:'disabled',selectable:false},
 {id:'unknown',name:'未知环境',description:'需检查',enabled:true,isDefault:false,state:'unknown',selectable:false}
]};}
function fixture(storage={}){
 const dom=new JSDOM(fs.readFileSync(path.join(root,'web/index.html'),'utf8'),{url:'http://127.0.0.1:3220',runScripts:'outside-only'}),w=dom.window,ctx=dom.getInternalVMContext();
 w.HTMLMediaElement.prototype.pause=function(){};w.HTMLMediaElement.prototype.load=function(){};
 w.HTMLDialogElement.prototype.showModal=function(){this.open=true;};w.HTMLDialogElement.prototype.close=function(){this.open=false;};
 w.FormData=FormData;w.confirm=()=>true;w.setInterval=()=>0;
 for(const[key,value]of Object.entries(storage))w.localStorage.setItem(key,value);
 const state={catalog:catalog(),calls:[],projects:new Map(),receipts:new Map(),catalogRead:null,failProjectRead:false,failList:false,loseCreate:false,postGate:null};
 const project=(id,body)=>({id,name:body.name,revision:1,productionProfile:Object.hasOwn(body,'productionProfile')?body.productionProfile:state.catalog.defaultProfile,creationRequestId:body.requestId,narrationText:'',workflow:{instructions:'',skills:['video-narration','video-editing','video-review']},assets:[],renders:[]});
 w.fetch=async(route,options={})=>{
  const pathname=new URL(route,w.location.href).pathname,method=options.method||'GET';state.calls.push({pathname,method,body:options.body?JSON.parse(options.body):undefined});
  if(pathname==='/api/runtime-profiles')return state.catalogRead?state.catalogRead():json(state.catalog);
  if(pathname==='/api/environment')return json({rundeskURL:'http://127.0.0.1:3210'});
  if(pathname==='/api/projects'&&method==='GET'){if(state.failList)throw Error('project list unavailable');return json([...state.projects.values()]);}
  if(pathname==='/api/projects'&&method==='POST'){
   const body=JSON.parse(options.body);let saved=state.receipts.get(body.requestId);
   if(!saved){saved=project((state.projects.size+1).toString(16).padStart(24,'0'),body);state.projects.set(saved.id,saved);state.receipts.set(body.requestId,saved);}
   if(state.postGate)await state.postGate;
   if(state.loseCreate){state.loseCreate=false;throw TypeError('creation response lost');}return json(saved,201);
  }
  const match=pathname.match(/^\/api\/projects\/([a-f0-9]{24})(.*)$/);
  if(match){if(match[2]===''){if(state.failProjectRead)throw Error('project read unavailable');return json(state.projects.get(match[1])||{});}if(match[2]==='/jobs')return json({jobs:[]});if(match[2]==='/production-board')return json({stages:[]});if(match[2]==='/agent')return json({status:'idle'});if(['/events','/approvals'].includes(match[2]))return json([]);}
  return json({});
 };
 const source=fs.readFileSync(path.join(root,'web/app.js'),'utf8'),boot=source.lastIndexOf('\nboot().catch(');assert.ok(boot>0);vm.runInContext(source.slice(0,boot),ctx);
 const run=code=>vm.runInContext(code,ctx),$=selector=>w.document.querySelector(selector);
 return{dom,w,state,run,$,plain:code=>JSON.parse(run('JSON.stringify('+code+')')),set:(selector,value)=>{$(selector).value=value;$(selector).dispatchEvent(new w.Event('input'));},choose:value=>{$('#new-profile').value=value;$('#new-profile').onchange();},posts:()=>state.calls.filter(row=>row.pathname==='/api/projects'&&row.method==='POST'),close:async()=>{await new Promise(resolve=>setImmediate(resolve));dom.window.close();}};
}

test('empty first launch opens an explicit environment picker without creating a project',async()=>{
 const f=fixture();try{
  await f.run('boot()');assert.equal(f.$('#new-dialog').open,true);assert.equal(f.posts().length,0);assert.equal(f.run('project'),null);
  assert.equal(f.$('#new-profile').value,'news');assert.equal(f.$('#new-submit').disabled,false);
  assert.match(f.$('#new-profile').textContent,/新闻视频 · 运行中 · 默认/);
  assert.equal(f.$('#new-profile option[value="retired"]').disabled,true);assert.equal(f.$('#new-profile option[value="unknown"]').disabled,true);
  assert.equal(new URL(f.$('#new-profile-rundesk').href).hash,'#application/rundesk-video-app');
 }finally{await f.close();}
});

test('search uses names and descriptions, preserves the chosen identity and treats names as text',async()=>{
 const f=fixture();try{
  f.state.catalog.items[2].name='<img src=x onerror=alert(1)>动画';await f.run('openNewProject()');
  f.set('#new-profile-search','图表');assert.equal(f.$('#new-profile').options.length,2,'the selected environment remains visible beside search matches');
  assert.equal(f.run('selectedRuntimeProfile'),'news');assert.ok(f.$('#new-profile option[value="animation"]'));assert.equal(f.$('#new-dialog img'),null);
  f.choose('animation');assert.equal(f.run('selectedRuntimeProfile'),'animation');
  f.set('#new-profile-search','no match');assert.equal(f.$('#new-profile-empty').hidden,false);assert.equal(f.$('#new-profile').value,'animation');assert.equal(f.$('#new-submit').disabled,false);
 }finally{await f.close();}
});

test('catalog failures and unavailable defaults never select a different environment',async()=>{
 const f=fixture();try{
  await f.run('openNewProject()');f.state.catalogRead=async()=>{throw Error('RunDesk permission unavailable');};await f.run('loadRuntimeProfiles()');
  assert.equal(f.run('selectedRuntimeProfile'),'news');assert.equal(f.$('#new-submit').disabled,true);assert.match(f.$('#new-profile-status').textContent,/permission unavailable/);
  await assert.rejects(f.run('createSelectedProject({preventDefault(){}})'),/选择可用/);assert.equal(f.posts().length,0);
  f.state.catalogRead=null;f.state.catalog.items.find(row=>row.id==='news').selectable=false;f.state.catalog.items.find(row=>row.id==='news').state='missing';
  await f.run('loadRuntimeProfiles()');assert.equal(f.run('selectedRuntimeProfile'),'news');assert.equal(f.$('#new-submit').disabled,true);
  f.choose('retired');assert.equal(f.$('#new-submit').disabled,true);f.choose('animation');assert.equal(f.$('#new-submit').disabled,false);
 }finally{await f.close();}
});

test('late refresh and closed-dialog responses cannot replace a newer catalog or choice',async()=>{
 const f=fixture();try{
  await f.run('openNewProject()');const pending=[];f.state.catalogRead=()=>new Promise(resolve=>pending.push(resolve));
  const old=f.run('loadRuntimeProfiles()'),fresh=f.run('loadRuntimeProfiles()');
  const newer=catalog();newer.defaultProfile='animation';newer.items.find(row=>row.id==='news').name='新的新闻环境';pending[1](json(newer));await fresh;
  assert.equal(f.run('selectedRuntimeProfile'),'news');assert.match(f.$('#new-profile-detail').textContent,/新的新闻环境/);
  const older=catalog();older.items.find(row=>row.id==='news').name='过期名称';pending[0](json(older));await old;assert.doesNotMatch(f.$('#new-profile-detail').textContent,/过期名称/);
  const closed=f.run('loadRuntimeProfiles()');f.run('closeNewProject()');pending[2](json(older));await closed;assert.equal(f.$('#new-dialog').open,false);assert.doesNotMatch(f.$('#new-profile-detail').textContent,/过期名称/);
 }finally{await f.close();}
});

test('explicit legacy default is sent as an empty profile and double submission creates once',async()=>{
 const f=fixture();try{
  await f.run('openNewProject()');f.choose('');f.set('#new-name','原环境项目');let release;f.state.postGate=new Promise(resolve=>{release=resolve;});
  const first=f.run('createSelectedProject({preventDefault(){}})');await f.run('createSelectedProject({preventDefault(){}})');assert.equal(f.posts().length,1);assert.equal(f.posts()[0].body.productionProfile,'');
  release();await first;assert.equal(f.state.projects.size,1);assert.equal(f.run('project.productionProfile'),'');assert.equal(f.w.localStorage.getItem(createKey),null);
 }finally{await f.close();}
});

test('lost creation response retains exact name, environment and request ID across dialog reopen',async()=>{
 const f=fixture();try{
  await f.run('openNewProject()');f.set('#new-name','原创建名称');f.choose('animation');f.state.loseCreate=true;
  await assert.rejects(f.run('createSelectedProject({preventDefault(){}})'),/response lost/);const body=copy(f.posts()[0].body);
  assert.deepEqual(JSON.parse(f.w.localStorage.getItem(createKey)),body);assert.equal(f.$('#new-name').disabled,true);
  f.run('closeNewProject()');f.state.catalog.defaultProfile='news';f.state.catalog.items.find(row=>row.id==='animation').selectable=false;
  await f.run('openNewProject()');assert.equal(f.$('#new-name').value,'原创建名称');assert.equal(f.run('selectedRuntimeProfile'),'animation');assert.equal(f.$('#new-profile').disabled,true);assert.equal(f.$('#new-submit').disabled,false,'a fixed receipt can be checked even if its former choice is no longer selectable');
  await f.run('createSelectedProject({preventDefault(){}})');assert.deepEqual(f.posts()[1].body,body);assert.equal(f.state.projects.size,1);assert.equal(f.run('project.productionProfile'),'animation');
 }finally{await f.close();}
});

test('a confirmed creation followed by failed loading is retried with the same receipt',async()=>{
 const f=fixture();try{
  await f.run('openNewProject()');f.state.failProjectRead=true;
  await assert.rejects(f.run('createSelectedProject({preventDefault(){}})'),/项目已创建/);const original=copy(f.posts()[0].body);
  assert.equal(f.$('#new-dialog').open,true);assert.deepEqual(JSON.parse(f.w.localStorage.getItem(createKey)),original);assert.equal(f.$('#new-name').disabled,true);
  f.state.failProjectRead=false;await f.run('createSelectedProject({preventDefault(){}})');assert.deepEqual(f.posts()[1].body,original);assert.equal(f.state.projects.size,1);
 }finally{await f.close();}
});

test('legacy pending requests without a profile keep their original body even when listing fails',async()=>{
 const original={name:'旧请求',requestId:'b'.repeat(24)},f=fixture({[legacyKey]:JSON.stringify(original)});try{
  f.state.catalogRead=async()=>{throw Error('catalog offline');};await f.run('openNewProject()');assert.equal(f.$('#new-submit').disabled,false);assert.equal(f.$('#new-name').value,'旧请求');assert.match(f.$('#new-profile-detail').textContent,/原请求未显式指定/);
  await f.run('createSelectedProject({preventDefault(){}})');assert.deepEqual(f.posts()[0].body,original);assert.equal(Object.hasOwn(f.posts()[0].body,'productionProfile'),false);assert.equal(f.w.localStorage.getItem(legacyKey),null);
 }finally{await f.close();}
});

test('unbound text survives choosing an environment and clearing text never revives an old draft',async()=>{
 const f=fixture();try{
  f.set('#narration-text','还没有项目的正文');f.set('#workflow-instructions','保留原文');f.set('#prompt','调整草稿');
  await assert.rejects(f.run('ensureProject()'),/先选择制作环境/);assert.equal(f.posts().length,0);assert.equal(f.$('#narration-text').value,'还没有项目的正文');
  await f.run('createSelectedProject({preventDefault(){}})');assert.equal(f.$('#narration-text').value,'还没有项目的正文');assert.equal(f.$('#workflow-instructions').value,'保留原文');assert.equal(f.$('#prompt').value,'调整草稿');assert.equal(f.run('narrationDirty'),true);assert.equal(f.run('project.narrationText'),'');assert.equal(f.w.localStorage.getItem('video-unbound-draft-v1'),null);
 }finally{await f.close();}
 const empty=fixture();try{
  empty.set('#narration-text','随后删除的文字');assert.ok(empty.w.localStorage.getItem('video-unbound-draft-v1'));empty.set('#narration-text','');assert.equal(empty.w.localStorage.getItem('video-unbound-draft-v1'),null);
  await empty.run('openNewProject()');await empty.run('createSelectedProject({preventDefault(){}})');assert.equal(empty.$('#narration-text').value,'');
 }finally{await empty.close();}
});

test('existing project bindings remain fixed and friendly names survive subsequent redraws',async()=>{
 const f=fixture();try{
  const existing={id:'a'.repeat(24),name:'已有项目',revision:1,productionProfile:'animation',runtimeBinding:{instanceId:'fixed-instance',workspaceId:'fixed-workspace',managedByRunDesk:true},workflow:{},assets:[],renders:[]};
  f.state.projects.set(existing.id,copy(existing));await f.run('openProject('+JSON.stringify(existing.id)+')');await new Promise(resolve=>setImmediate(resolve));
  assert.match(f.$('#project-runtime').textContent,/原默认环境/);f.run('drawProject()');assert.match(f.$('#project-runtime').textContent,/原默认环境/);
  const before=f.plain('project.runtimeBinding');await f.run('openNewProject()');f.choose('news');f.run('closeNewProject();drawProject()');assert.deepEqual(f.plain('project.runtimeBinding'),before);assert.match(f.$('#project-runtime').textContent,/原默认环境/);assert.equal(f.posts().length,0);
 }finally{await f.close();}
});


test('friendly saved labels remain stable on redraw and an old project response cannot overwrite the current project',async()=>{
 const f=fixture();try{
  const make=(id,profile)=>({id,name:'已有项目',revision:1,productionProfile:profile,workflow:{},assets:[],renders:[]});
  const first=make('c'.repeat(24),'news'),second=make('d'.repeat(24),'animation');f.state.projects.set(first.id,first);f.state.projects.set(second.id,second);
  await f.run('openProject('+JSON.stringify(first.id)+')');await new Promise(resolve=>setImmediate(resolve));f.run('drawProject()');assert.match(f.$('#project-runtime').textContent,/新闻视频/);
  const pending=[];f.state.catalogRead=()=>new Promise(resolve=>pending.push(resolve));
  await f.run('openProject('+JSON.stringify(first.id)+')');await f.run('openProject('+JSON.stringify(second.id)+')');
  const newer=catalog();newer.items.find(row=>row.id==='animation').name='当前动画环境';pending[1](json(newer));await new Promise(resolve=>setImmediate(resolve));
  pending[0](json(catalog()));await new Promise(resolve=>setImmediate(resolve));f.run('drawProject()');assert.match(f.$('#project-runtime').textContent,/当前动画环境/);assert.doesNotMatch(f.$('#project-runtime').textContent,/新闻视频/);
 }finally{await f.close();}
});
