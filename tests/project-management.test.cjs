'use strict';
const {test}=require('node:test'),assert=require('node:assert/strict'),fs=require('node:fs'),path=require('node:path'),vm=require('node:vm'),{JSDOM}=require('jsdom');
const root=path.resolve(__dirname,'..'),a='a'.repeat(24),b='b'.repeat(24);
const json=(v,status=200)=>new Response(JSON.stringify(v),{status,headers:{'Content-Type':'application/json'}});
function fixture(){
 const dom=new JSDOM(fs.readFileSync(path.join(root,'web/settings.html'),'utf8'),{url:'http://localhost:3220/settings.html',runScripts:'outside-only'}),w=dom.window,ctx=dom.getInternalVMContext();
 w.HTMLDialogElement.prototype.showModal=function(){this.open=true;};w.HTMLDialogElement.prototype.close=function(){this.open=false;};
 const state={items:[{id:a,name:'归档新闻',archived:true,revision:4,status:'completed',statusLabel:'已出片'},{id:b,name:'正在制作',archived:false,revision:2,status:'running',statusLabel:'制作中'}],calls:[],failFirst:false};
 w.fetch=async(route,opts={})=>{const url=new URL(route,w.location.href),method=opts.method||'GET',body=opts.body?JSON.parse(opts.body):undefined;state.calls.push({url:url.pathname,method,body});
  if(url.pathname==='/api/projects'&&method==='GET')return json(state.items);
  if(url.pathname==='/api/projects/'+a&&method==='DELETE'){if(state.failFirst){state.failFirst=false;return json({error:'revision conflict'},409);}state.items=state.items.filter(row=>row.id!==a);return json({ok:true,id:a});}
  if(url.pathname==='/api/projects/'+b+'/management'&&method==='PATCH')return json({error:'制作仍在运行'},409);
  return json({});};
 const source=fs.readFileSync(path.join(root,'web/settings.js'),'utf8'),boot=source.lastIndexOf('\nbootSettings().catch(');assert.ok(boot>0);vm.runInContext(source.slice(0,boot),ctx);
 const run=code=>vm.runInContext(code,ctx),$=s=>w.document.querySelector(s);
 return{dom,w,state,run,$,close:()=>w.close()};
}
test('bulk deletion is restricted to archived projects and requires exact typed confirmation',async()=>{
 const f=fixture();try{
  await f.run('loadManagedProjects()');
  for(const id of [a,b]){const input=f.$(`input[data-project-id="${id}"]`);input.checked=true;input.dispatchEvent(new f.w.Event('change'));}
  assert.equal(f.$('#managed-project-delete').disabled,true);
  f.$(`input[data-project-id="${b}"]`).checked=false;f.$(`input[data-project-id="${b}"]`).dispatchEvent(new f.w.Event('change'));
  assert.equal(f.$('#managed-project-delete').disabled,false);
  f.run('openProjectDeleteDialog()');assert.equal(f.$('#project-delete-dialog').open,true);assert.equal(f.$('#project-delete-submit').disabled,true);
  f.$('#project-delete-confirm').value='删除 1 个项目';f.$('#project-delete-confirm').dispatchEvent(new f.w.Event('input'));
  assert.equal(f.$('#project-delete-submit').disabled,false);
  assert.equal(f.state.calls.some(row=>row.method==='DELETE'),false);
  await f.$('#project-delete-form').onsubmit({preventDefault(){}});
  const deleted=f.state.calls.filter(row=>row.method==='DELETE');assert.equal(deleted.length,1);assert.deepEqual(deleted[0].body,{revision:4,confirmName:'归档新闻'});
  assert.equal(f.$('#managed-project-results').textContent.includes('归档新闻：删除成功'),true);
 }finally{f.close();}
});
test('a failed delete retains its selection and reports the per-project conflict',async()=>{
 const f=fixture();try{
  f.state.failFirst=true;await f.run('loadManagedProjects()');const input=f.$(`input[data-project-id="${a}"]`);input.checked=true;input.dispatchEvent(new f.w.Event('change'));
  await f.run('applyManagedProjectAction("delete",selectedManagedProjects())');
  assert.match(f.$('#managed-project-results').textContent,/删除失败.*revision conflict/);
  assert.equal(f.state.items.length,2);assert.equal(f.$(`input[data-project-id="${a}"]`).checked,true);
 }finally{f.close();}
});
