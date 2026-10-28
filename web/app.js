"use strict";
const TOOL_VERSION = "0.28.0";
const $ = s => document.querySelector(s);
const el = (tag, text, cls) => { const node=document.createElement(tag); if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node; };
let project=null, epoch=0, session=null, after=0, rows=new Map(), approvalKey="", polling=false, sending=false, startingDraft=false, uploading=false;
let narrationDirty=false,narrationBase=0,workflowDirty=false,workflowBase=0,boardLoading=false,boardKey="",serviceJobsKey="",serviceJobsLoading=false,activeProductionJobs=[];
let rundeskURL="", draftStorageError="";
let page="make",projectsCache=[],productionState=null,productionStateLoading=false,productionStateSerial=0,editingInput=false,reviewTimeline=null,reviewAnchor=null,previewURL="",previewKey="",pendingRecoveryAt=new Map();
const api=async(path,body,method)=>{const options={method:method||(body===undefined?"GET":"POST")};if(body!==undefined){options.body=body instanceof FormData?body:JSON.stringify(body);if(!(body instanceof FormData))options.headers={"Content-Type":"application/json"};}const response=await fetch(path,options);const data=await response.json();if(!response.ok){const error=new Error(data.error||response.statusText);Object.assign(error,{status:response.status,code:data.code,uncertain:data.uncertain});throw error;}return data;};
const path=(tail="")=>"/api/projects/"+project.id+tail;
function id(){return [...crypto.getRandomValues(new Uint8Array(12))].map(value=>value.toString(16).padStart(2,"0")).join("");}
function toast(message){$("#toast").textContent=message;$("#toast").hidden=false;clearTimeout(toast.timer);toast.timer=setTimeout(()=>$("#toast").hidden=true,6000);}
function action(fn){return async event=>{try{return await fn(event);}catch(error){toast(error.message);}};}
const receiptProblems=new Map(),stoppingProjects=new Set();
function pendingMessage(pid){
 const key="video-message:"+pid;let raw;
 try{raw=localStorage.getItem(key);if(!raw){try{raw=sessionStorage.getItem(key);}catch{}}if(!raw){receiptProblems.delete(pid);return null;}const value=JSON.parse(raw);if(!value||typeof value.requestId!=="string"||typeof value.text!=="string")throw Error("记录格式不完整");receiptProblems.delete(pid);return value;}
 catch(error){receiptProblems.set(pid,"本机提交记录无法读取，请在 RunDesk 核对原制作任务后恢复："+error.message);return null;}
}
function rememberMessage(pid,body){const key="video-message:"+pid;if(body)localStorage.setItem(key,JSON.stringify(body));else localStorage.removeItem(key);try{sessionStorage.removeItem(key);}catch{}receiptProblems.delete(pid);}
function clearMessage(pid,requestId){const pending=pendingMessage(pid);if(!pending||pending.requestId!==requestId)return false;rememberMessage(pid,null);return true;}

function persistDraft(){
 if(!project){try{rememberUnboundDraft();}catch(error){draftStorageError="浏览器未能保存本机草稿："+error.message;}return;}
 try{const draft={prompt:$("#prompt").value};if(narrationDirty)draft.narration={text:$("#narration-text").value,baseRevision:narrationBase};if(workflowDirty)draft.workflow={instructions:$("#workflow-instructions").value,baseRevision:workflowBase};const key="video-draft-v1:"+project.id;if(draft.prompt||draft.narration||draft.workflow)localStorage.setItem(key,JSON.stringify(draft));else localStorage.removeItem(key);draftStorageError="";}
 catch(error){draftStorageError="浏览器未能保存本机草稿："+error.message;$("#save-state").textContent=draftStorageError;}
}
function restoreDraft(){
 let draft;try{draft=JSON.parse(localStorage.getItem("video-draft-v1:"+project.id)||"null");}catch(error){toast("本机草稿暂不可读取："+error.message);return;}
 if(!draft)return;
 if(draft.narration&&typeof draft.narration.text==="string"){narrationDirty=true;narrationBase=draft.narration.baseRevision;$("#narration-text").value=draft.narration.text;}
 if(draft.workflow&&typeof draft.workflow.instructions==="string"){workflowDirty=true;workflowBase=draft.workflow.baseRevision;$("#workflow-instructions").value=draft.workflow.instructions;}
 if(typeof draft.prompt==="string")$("#prompt").value=draft.prompt;
 $("#conflict").hidden=!(narrationDirty&&narrationBase!==project.revision||workflowDirty&&workflowBase!==project.revision);
}
function hasUnsavedEdits(){return narrationDirty||workflowDirty||!!$("#prompt").value.trim();}
function advanceIndependentBases(previous,next){if(narrationDirty&&narrationBase===previous)narrationBase=next;if(workflowDirty&&workflowBase===previous)workflowBase=next;}
function reconcileMessage(submission){
 if(!project)return null;
 let pending=pendingMessage(project.id);
 if(!pending&&submission?.state==="pending"){pending={text:submission.text,requestId:submission.id};if(submission.purpose)pending.purpose=submission.purpose;if(Array.isArray(submission.skills))pending.skills=[...submission.skills];if(submission.turnId)pending.expectedTurnId=submission.turnId;rememberMessage(project.id,pending);}
 if(pending&&submission?.id===pending.requestId&&["accepted","failed","canceled"].includes(submission.state)){
  clearMessage(project.id,pending.requestId);
  if(submission.state==="accepted"&&pending.text.startsWith($("#prompt").value.trim())&&$("#prompt").value.trim())$("#prompt").value="";
  $("#send-state").textContent=submission.state==="accepted"?"调整已接收":submission.error||"原请求已结束";pending=null;persistDraft();
 }
 if(pending){$("#send-state").textContent=submission?.cancelRequested?"正在确认停止，不会重新发起制作":"正在核对原制作请求，输入和选择已保留。";$("#agent-stop").hidden=false;}
 drawWorkflow();return pending;
}
function showApp(){$("#login").hidden=true;$("#app").hidden=false;}
function projectStatus(item){
 if(item.statusLabel)return item.statusLabel;
 if(item.final||item.production?.stage==="final")return "已确认终稿";
 if(item.agentSubmission?.state==="pending")return "制作中";
 if(item.renders?.some(render=>render.state==="completed"&&!render.range))return "已出片";
 return "待制作";
}
function projectCard(item){
 const button=el("button",undefined,"project-card"+(project?.id===item.id?" selected":""));button.type="button";button.dataset.id=item.id;
 const status=projectStatus(item),dot=el("span",status==="制作中"?"🟢":status==="已确认终稿"?"✅":status==="已出片"?"⚪":"◌","project-dot");dot.setAttribute("aria-label",status);
 button.append(dot,el("span",item.name,"project-card-name"),el("span",status,"project-card-status"));
 button.onclick=action(async()=>{if(item.id!==project?.id)await openProject(item.id);else setPage("make");});return button;
}
function drawProjectList(){
 const active=projectsCache.filter(item=>!item.archived),archived=projectsCache.filter(item=>item.archived),query=$("#project-search").value.trim().toLocaleLowerCase();
 const order=(a,b)=>String(b.lastOpened||b.updated||"").localeCompare(String(a.lastOpened||a.updated||""));active.sort(order);archived.sort(order);
 const matched=active.filter(item=>item.name.toLocaleLowerCase().includes(query));
 const recent=query?matched:matched.slice(0,5),older=query?[]:active.slice(5);
 const box=$("#projects");box.replaceChildren(...recent.map(projectCard));
 $("#project-list-empty").hidden=recent.length>0;$("#project-list-empty").textContent=query?"没有匹配的项目。":"还没有项目，先新建一个吧。";
 $("#project-recent-label").textContent=query?"搜索结果":"最近打开";
 const all=$("#project-all"),allBox=$("#project-all-list");all.hidden=!!query||!older.length;allBox.replaceChildren(...older.map(projectCard));
 const archive=$("#project-archive"),archiveBox=$("#project-archive-list");archive.hidden=!archived.length;archiveBox.replaceChildren(...archived.filter(item=>item.name.toLocaleLowerCase().includes(query)).map(projectCard));
}
async function listProjects(){const list=await api("/api/projects");projectsCache=Array.isArray(list)?list:[];drawProjectList();return projectsCache;}
function getPageFromURL(){return new URL(location.href).searchParams.get("page")==="refine"?"refine":"make";}
function setPage(next,replace=false){
 page=next==="refine"?"refine":"make";
 $("#page-make").hidden=page!=="make";$("#page-refine").hidden=page!=="refine";
 $("#page-make-link").classList.toggle("active",page==="make");$("#page-refine-link").classList.toggle("active",page==="refine");
 $("#page-make-link").setAttribute("aria-current",page==="make"?"page":"false");$("#page-refine-link").setAttribute("aria-current",page==="refine"?"page":"false");
 const slot=$(page==="make"?"#make-output-slot":"#refine-output-slot");if($("#outputs").parentNode!==slot)slot.append($("#outputs"));
 const progressSlot=$(page==="make"?"#make-progress-slot":"#refine-progress-slot")||$("#page-make");if($("#production-results").parentNode!==progressSlot)progressSlot.append($("#production-results"));
 $("#output-version-field").hidden=page==="make";$("#output-final").hidden=page==="make"||$("#output-final").hidden;
 if(page==="refine")drawOutputs(true);
 const url=new URL(location.href);if(project)url.searchParams.set("project",project.id);else url.searchParams.delete("project");url.searchParams.set("page",page);
 if(url.href!==location.href)history[replace?"replaceState":"pushState"]({},"",url.href);
 drawProductionView();drawProjectList();
}
const projectCreateKey="video-project-create-v2", legacyProjectCreateKey="video-default-project-v1";
let runtimeCatalog=null, runtimeCatalogReady=false, runtimeCatalogSerial=0, newDialogSerial=0,projectRuntimeCatalog=null,unboundDraftLoaded=false;
let selectedRuntimeProfile, newProjectReceipt=null, newProjectReceiptError="", creatingProject=false;
const runtimeStateLabels={running:"运行中",stopped:"已停止",not_started:"未启动",local:"本机环境",pending_update:"等待更新",disabled:"已停用",unknown:"状态未知",unavailable:"暂不可用",missing:"尚未配置"};
function rememberUnboundDraft(){
 if(project)return;
 const draft={narration:$("#narration-text").value,instructions:$("#workflow-instructions").value,prompt:$("#prompt").value};
 if(Object.values(draft).some(Boolean)){localStorage.setItem("video-unbound-draft-v1",JSON.stringify(draft));unboundDraftLoaded=true;}else if(unboundDraftLoaded||narrationDirty||workflowDirty)localStorage.removeItem("video-unbound-draft-v1");
}
function restoreUnboundDraft(){
 let draft;try{draft=JSON.parse(localStorage.getItem("video-unbound-draft-v1")||"null");}catch{return;}
 if(!draft)return;unboundDraftLoaded=true;
 for(const [field,selector] of [["narration","#narration-text"],["instructions","#workflow-instructions"],["prompt","#prompt"]])if(typeof draft[field]==="string")$(selector).value=draft[field];
 if(project){narrationDirty=!!draft.narration;workflowDirty=!!draft.instructions;narrationBase=workflowBase=project.revision;persistDraft();localStorage.removeItem("video-unbound-draft-v1");drawProject();}
}
function readProjectCreationReceipt(){
 const saved=localStorage.getItem(projectCreateKey),legacy=localStorage.getItem(legacyProjectCreateKey);
 if(saved&&legacy)throw Error("发现两份待确认的创建记录，请先核对原项目，未发起新建。");
 const raw=saved||legacy;if(!raw)return null;
 let body;try{body=JSON.parse(raw);}catch{throw Error("本机项目创建记录无法读取，未发起新的创建请求。");}
 if(!body||typeof body.name!=="string"||typeof body.requestId!=="string"||!/^[a-f0-9]{24}$/.test(body.requestId)||Object.keys(body).some(key=>!["name","requestId","productionProfile"].includes(key))||Object.hasOwn(body,"productionProfile")&&typeof body.productionProfile!=="string")throw Error("本机项目创建记录不完整，未发起新的创建请求。");
 return{key:saved?projectCreateKey:legacyProjectCreateKey,body};
}
const rundeskBrowserKey="video-rundesk-browser-url";
function browserRunDeskURL(fallback){
 let value="";try{value=localStorage.getItem(rundeskBrowserKey)||"";}catch{}
 if(!value){try{const host=new URL(fallback).hostname;if(["localhost","127.0.0.1","::1","[::1]"].includes(host)&&!["localhost","127.0.0.1","::1","[::1]"].includes(location.hostname))return "";value=fallback;}catch{return "";}}
 try{const url=new URL(value);if(!["http:","https:"].includes(url.protocol)||url.username||url.password||url.search||url.hash)return "";return url.href.replace(/\/$/,"");}catch{return "";}
}
function setRuntimeConfigLink(){
 const link=$("#new-profile-rundesk");link.hidden=true;
 try{const address=new URL(browserRunDeskURL(runtimeCatalog?.rundeskURL||rundeskURL));if(!["http:","https:"].includes(address.protocol)||address.username||address.password)return;address.search="";address.hash="application/rundesk-video-app";link.href=address.href;link.hidden=false;}catch{}
}
function updateNewProjectControls(){
 const item=runtimeCatalog?.items.find(row=>row.id===selectedRuntimeProfile),locked=creatingProject||!!newProjectReceipt||!!newProjectReceiptError;
 $("#new-name").disabled=locked;$("#new-profile-search").disabled=locked||!runtimeCatalogReady;$("#new-profile").disabled=locked||!runtimeCatalogReady;
 $("#new-submit").disabled=creatingProject||!!newProjectReceiptError||!newProjectReceipt&&(!runtimeCatalogReady||!item?.selectable||!item.enabled);
 $("#new-submit").textContent=creatingProject?"正在创建…":newProjectReceipt?"确认原创建":"创建项目";
 $("#new-profile-receipt").hidden=!newProjectReceipt;
 $("#new-profile-receipt").textContent=newProjectReceipt?"已保存原创建请求。名称和环境保持不变，重试只核对或打开同一个项目。":"";
 setRuntimeConfigLink();
}
function renderRuntimeChoices(){
 const select=$("#new-profile"),query=$("#new-profile-search").value.trim().toLocaleLowerCase(),items=runtimeCatalog?.items||[];
 const matches=items.filter(item=>(item.name+" "+(item.description||"")).toLocaleLowerCase().includes(query));
 const selected=items.find(item=>item.id===selectedRuntimeProfile),visible=[...matches];
 if(selected&&!visible.includes(selected))visible.unshift(selected);
 select.replaceChildren();
 for(const item of visible){const option=el("option",item.name+" · "+(runtimeStateLabels[item.state]||"状态未知")+(item.isDefault?" · 默认":""));option.value=item.id;option.disabled=!item.selectable||!item.enabled;select.append(option);}
 if(selected){select.value=selected.id;}else{select.selectedIndex=-1;}
 const detail=$("#new-profile-detail");
 if(newProjectReceipt&&!Object.hasOwn(newProjectReceipt.body,"productionProfile"))detail.textContent="原请求未显式指定环境，重试保留原始内容，不改用当前默认值。";
 else if(selected)detail.textContent=selected.name+(selected.description?" · "+selected.description:"")+(selected.message?" · "+selected.message:"");
 else if(selectedRuntimeProfile!==undefined)detail.textContent="原选环境暂不可用，请刷新或选择其他环境。";
 else detail.textContent="选择 RunDesk 中已为此应用配置的制作环境。";
 $("#new-profile-empty").hidden=matches.length>0;
 $("#new-profile-empty").textContent=query?"没有匹配的环境；已有选择保持不变。":"没有可选制作环境，请在 RunDesk 中配置后刷新。";
 updateNewProjectControls();
}
async function loadRuntimeProfiles(){
 const serial=++runtimeCatalogSerial,dialogSerial=newDialogSerial;runtimeCatalogReady=false;updateNewProjectControls();
 $("#new-profile-status").classList.remove("error");$("#new-profile-status").textContent="正在读取 RunDesk 制作环境…";
 try{
  const catalog=await api("/api/runtime-profiles");
  if(serial!==runtimeCatalogSerial||dialogSerial!==newDialogSerial||!$("#new-dialog").open)return;
  if(!catalog||!Array.isArray(catalog.items)||typeof catalog.defaultProfile!=="string"||catalog.items.some(item=>!item||typeof item.id!=="string"||typeof item.name!=="string"||typeof item.enabled!=="boolean"||typeof item.selectable!=="boolean")||new Set(catalog.items.map(item=>item.id)).size!==catalog.items.length)throw Error("RunDesk 制作环境列表格式无效，未更换原选择。");
  runtimeCatalog=catalog;runtimeCatalogReady=true;
  if(newProjectReceipt){selectedRuntimeProfile=Object.hasOwn(newProjectReceipt.body,"productionProfile")?newProjectReceipt.body.productionProfile:undefined;}
  else if(selectedRuntimeProfile===undefined)selectedRuntimeProfile=catalog.defaultProfile;
  $("#new-profile-status").textContent=(catalog.warnings||[]).filter(item=>typeof item==="string").join(" ")||"已读取环境。未启动的环境会在开始制作时按原流程准备。";
  renderRuntimeChoices();
 }catch(error){
  if(serial!==runtimeCatalogSerial||dialogSerial!==newDialogSerial||!$("#new-dialog").open)return;
  runtimeCatalogReady=false;$("#new-profile-status").classList.add("error");$("#new-profile-status").textContent=error.message+" 请刷新重试，或打开 RunDesk 配置。";updateNewProjectControls();
 }
}
async function openNewProject(){
 persistDraft();++newDialogSerial;newProjectReceiptError="";
 try{newProjectReceipt=readProjectCreationReceipt();}catch(error){newProjectReceipt=null;newProjectReceiptError=error.message;}
 if(newProjectReceipt){$("#new-name").value=newProjectReceipt.body.name;selectedRuntimeProfile=Object.hasOwn(newProjectReceipt.body,"productionProfile")?newProjectReceipt.body.productionProfile:undefined;}
 else if(!$("#new-name").value)$("#new-name").value="新视频";
 $("#new-project-error").textContent=newProjectReceiptError;$("#new-profile-search").value="";
 if(!$("#new-dialog").open)$("#new-dialog").showModal();
 renderRuntimeChoices();await loadRuntimeProfiles();
}
function closeNewProject(){++newDialogSerial;++runtimeCatalogSerial;$("#new-dialog").close();}
async function createSelectedProject(event){
 event.preventDefault();if(creatingProject||newProjectReceiptError)return;
 persistDraft();const gen=epoch,carryUnbound=!project;
 if(!newProjectReceipt){
  const selected=runtimeCatalog?.items.find(item=>item.id===selectedRuntimeProfile);
  if(!runtimeCatalogReady||!selected?.selectable||!selected.enabled)throw Error("请先从已读取的列表选择可用制作环境。");
  const name=$("#new-name").value.trim();if(!name)throw Error("请填写项目名称。");
  const body={name,productionProfile:selectedRuntimeProfile,requestId:id()};localStorage.setItem(projectCreateKey,JSON.stringify(body));newProjectReceipt={key:projectCreateKey,body};
 }
 creatingProject=true;let acknowledged=false;$("#new-project-error").textContent="";updateNewProjectControls();
 try{
  const receipt=newProjectReceipt,created=await api("/api/projects",receipt.body);
  if(!created||!/^[a-f0-9]{24}$/.test(created.id)||Object.hasOwn(receipt.body,"productionProfile")&&created.productionProfile!==receipt.body.productionProfile)throw Error("项目创建回执与所选环境不一致，原请求已保留，请重试核对。");
  acknowledged=true;
  if(gen===epoch){await openProject(created.id);if(carryUnbound)restoreUnboundDraft();}
  await listProjects();
  let saved;try{saved=JSON.parse(localStorage.getItem(receipt.key)||"null");}catch{}
  if(saved?.requestId===receipt.body.requestId)localStorage.removeItem(receipt.key);
  newProjectReceipt=null;selectedRuntimeProfile=undefined;$("#new-name").value="";closeNewProject();
 }catch(error){const message=error.message+(acknowledged?" 项目已创建，重试会打开同一个项目。":" 原创建请求已保留，可重试核对。");$("#new-project-error").textContent=message;throw Error(message);}
 finally{creatingProject=false;updateNewProjectControls();}
}
function drawProjectRuntime(catalog){
 const label=$("#project-runtime");if(!project){label.textContent="";return;}
 const profile=project.runtimeBinding?(project.runtimeBinding.presetId||""):project.productionProfile;
 const source=catalog||projectRuntimeCatalog||runtimeCatalog,item=Array.isArray(source?.items)?source.items.find(row=>row.id===(profile||"")):undefined;
 const name=item?.name||(profile?"已保存的制作环境":"应用默认环境");
 label.textContent="制作环境："+name+" · 随项目保存"+(item?.state==="disabled"?" · 已停用":"");
}
async function loadProjectRuntimeLabel(){
 if(!project)return;const pid=project.id,gen=epoch;
 try{const catalog=await api("/api/runtime-profiles");if(gen===epoch&&project?.id===pid){projectRuntimeCatalog=catalog;drawProjectRuntime();}}catch{if(gen===epoch&&project?.id===pid)drawProjectRuntime();}
}
async function ensureProject(){if(project)return project;await openNewProject();throw Error("请先选择制作环境并创建项目，已输入的正文与要求会保留。");}
async function openProject(pid,{fromURL=false}={}){
 persistDraft();const gen=++epoch;let current;try{current=await api("/api/projects/"+pid);}catch(error){if(gen===epoch){sending=false;startingDraft=false;uploading=false;drawWorkflow();drawProjectList();}throw error;}if(gen!==epoch)return null;
 project=current;projectRuntimeCatalog=null;session=null;after=0;rows=new Map();approvalKey="";boardKey="";boardLoading=false;serviceJobsKey="";serviceJobsLoading=false;activeProductionJobs=[];narrationDirty=false;workflowDirty=false;narrationBase=current.revision;workflowBase=current.revision;sending=false;startingDraft=false;uploading=false;productionState=null;productionStateLoading=false;productionStateSerial++;editingInput=false;reviewTimeline=null;reviewAnchor=null;previewKey="";if(previewURL){URL.revokeObjectURL(previewURL);previewURL="";}
 $("#prompt").value="";$("#narration-text").value=current.narrationText||"";$("#workflow-instructions").value=current.workflow?.instructions||"";$("#conflict").hidden=true;$("#send-state").textContent="";$("#workflow-status").textContent="";$("#narration-status").textContent="";$("#approvals").replaceChildren();$("#conversation").replaceChildren(el("p",current.sessionId?"载入已有对话…":"开始制作后，在这里继续调整。","chat-empty"));
 resetOutputs();restoreDraft();drawProject();reconcileMessage(current.agentSubmission);loadProjectRuntimeLabel();
 if(!fromURL){setPage("make");}else setPage(getPageFromURL(),true);
 try{await api("/api/projects/"+pid+"/opened",{});if(gen===epoch){const item=projectsCache.find(row=>row.id===pid);if(item){item.lastOpened=new Date().toISOString();drawProjectList();}}}catch{}
 await poll();return gen===epoch?project:null;
}
function drawProject(){
 if(!project)return;
 $("#project-name").textContent=project.name;drawProjectRuntime();
 if(!narrationDirty){$("#narration-text").value=project.narrationText||"";narrationBase=project.revision;}
 if(!workflowDirty){$("#workflow-instructions").value=project.workflow?.instructions||"";workflowBase=project.revision;}
 $("#save-state").textContent=draftStorageError||((narrationDirty||workflowDirty)?"本机草稿已保留，开始制作或发送调整时自动保存":"已保存 · 修订 "+project.revision);
 $("#narration-use-existing").hidden=!!project.narrationText?.trim()||(!project.production?.text?.trim()&&!project.news);
 drawAssets();drawWorkflow();updateRunDeskLink();loadProductionBoard().catch(()=>{});loadProductionJobs().catch(()=>{});drawOutputs();loadProductionState().catch(()=>{});drawProjectList();
}
function drawWorkflow(){
 if(!project)return;
 const pending=pendingMessage(project.id);
 const receiptError=receiptProblems.get(project.id),stopping=stoppingProjects.has(project.id);
 if(receiptError)$("#workflow-status").textContent=receiptError;
 const busy=!!pending||["pending","running","unknown"].includes(productionState?.state);
 $("#draft-start").disabled=sending||startingDraft||uploading||stopping||busy||!!receiptError;
 $("#draft-start").textContent="开始制作";
 $("#agent-stop").disabled=stopping;
 $("#agent-stop").hidden=!(pending||activeProductionJobs.some(job=>["running","cancel_requested"].includes(job.state))||session&&["running","waiting","starting","stopping"].includes(session.status));
 $("#send").disabled=sending||startingDraft||stopping||!project.sessionId||!!receiptError;
 $("#send").textContent="发送调整";
 const counts={image:0,video:0,audio:0,other:0};for(const asset of project.assets||[]){const type=asset.mime?.split("/")[0];counts[type in counts?type:"other"]++;}
 $("#primary-material-summary").textContent=uploading?"正在导入素材…":project.assets?.length?`${counts.image} 张图片 · ${counts.video} 段视频 · ${counts.audio} 段音频`+(counts.other?` · ${counts.other} 份文件`:""):"可添加图片、视频和音频，也可先从正文开始。";
 drawProductionView();
}
function updateRunDeskLink(){const link=$("#open-rundesk");const address=browserRunDeskURL(rundeskURL);if(!address){link.hidden=true;return;}link.hidden=false;link.href=address+(project?.sessionId?"/?session="+encodeURIComponent(project.sessionId):"/#application/rundesk-video-app");}
async function saveNarration(){
 if(!project)return false;const pid=project.id,gen=epoch,text=$("#narration-text").value,previous=project.revision;
 try{const result=await api("/api/projects/"+pid+"/narration",{revision:narrationBase,text},"PUT");if(gen!==epoch||project?.id!==pid)return false;
  if(result.revision<project.revision&&project.narrationText!==result.narrationText){narrationDirty=true;narrationBase=result.revision;$("#conflict").hidden=false;persistDraft();drawProject();return false;}
  advanceIndependentBases(previous,result.revision);if(result.revision>=project.revision)project=result;narrationDirty=$("#narration-text").value!==text;narrationBase=result.revision;persistDraft();drawProject();return true;
 }catch(error){if(gen===epoch&&error.status===409)$("#conflict").hidden=false;throw error;}
}
async function saveWorkflow(){
 if(!project)return false;const pid=project.id,gen=epoch,previous=project.revision,instructions=$("#workflow-instructions").value;
 const workflow={...(project.workflow||{skills:["video-narration","video-editing","video-review"],editingBackend:"native"}),instructions};
 try{const result=await api("/api/projects/"+pid+"/workflow",{revision:workflowBase,workflow},"PUT");if(gen!==epoch||project?.id!==pid)return false;
  if(result.revision<project.revision&&project.workflow?.instructions!==result.workflow?.instructions){workflowDirty=true;workflowBase=result.revision;$("#conflict").hidden=false;persistDraft();drawProject();return false;}
  advanceIndependentBases(previous,result.revision);if(result.revision>=project.revision)project=result;workflowDirty=$("#workflow-instructions").value!==instructions;workflowBase=result.revision;persistDraft();drawProject();return true;
 }catch(error){if(gen===epoch&&error.status===409)$("#conflict").hidden=false;throw error;}
}

async function save(){const gen=epoch;if(narrationDirty&&!await saveNarration())return false;if(gen!==epoch)return false;if(workflowDirty&&!await saveWorkflow())return false;return gen===epoch;}
async function deliverMessage(pid,body){
 const gen=epoch;sending=true;drawWorkflow();
 try{await api("/api/projects/"+pid+"/messages",body);const cleared=clearMessage(pid,body.requestId);if(cleared&&gen===epoch&&project?.id===pid){if($("#prompt").value.trim()&&body.text.startsWith($("#prompt").value.trim()))$("#prompt").value="";$("#send-state").textContent="请求已接收";persistDraft();}}
 catch(error){if(!error.uncertain&&["submission_rejected","request_obsolete"].includes(error.code))clearMessage(pid,body.requestId);if(gen===epoch)$("#send-state").textContent=pendingMessage(pid)?"接收情况待确认，正在核对原请求。":error.message;throw error;}
 finally{if(gen===epoch){sending=false;drawWorkflow();}}
 if(gen===epoch){await poll();loadProductionState(true).catch(()=>{});}
}
async function startProduction(){
 await ensureProject();if(sending||startingDraft||uploading||stoppingProjects.has(project.id))return;const pid=project.id,gen=epoch;startingDraft=true;drawWorkflow();
 try{
  const pending=pendingMessage(pid);if(receiptProblems.has(pid))throw Error(receiptProblems.get(pid));if(pending){await deliverMessage(pid,pending);return;}
  if(session&&["running","waiting","starting","stopping"].includes(session.status))throw Error("当前正在制作，可在精修页查看进度。");
  $("#workflow-status").textContent="正在保存正文与制作要求…";if(!await save()||gen!==epoch)return;
  if(!$("#narration-text").value.trim())throw Error("请先粘贴或导入正文。");
  const instruction=await api("/api/projects/"+pid+"/draft/instruction",{revision:project.revision});if(gen!==epoch)return;
  if(project.sessionId){const currentSession=await api("/api/projects/"+pid+"/agent");if(gen!==epoch)return;session=currentSession;if(["running","waiting","starting","stopping"].includes(session.status))throw Error("当前正在制作，可在精修页查看进度。");}
  if(!project.sessionId||project.toolsVersion!==TOOL_VERSION){$("#workflow-status").textContent="正在准备创作助手…";const connected=await api("/api/projects/"+pid+"/connect"+(project.sessionId?"?refresh=1":""),{});if(gen!==epoch)return;project.sessionId=connected.sessionId;project.workspaceId=connected.workspaceId;project.toolsVersion=connected.toolsVersion;updateRunDeskLink();}
  if(narrationDirty||workflowDirty)throw Error("正文或要求又有修改，请再次点击开始制作。");
  const body={text:instruction.text,requestId:id(),purpose:"draft",skills:project.workflow?.skills||["video-narration","video-editing","video-review"]};rememberMessage(pid,body);editingInput=false;drawProductionView();await deliverMessage(pid,body);
  if(gen===epoch)$("#workflow-status").textContent="已开始制作。成果会显示在下方，未发送的调整草稿仍保留。";
 }catch(error){if(gen===epoch)$("#workflow-status").textContent=error.message;throw error;}finally{if(gen===epoch){startingDraft=false;drawWorkflow();}}
}
async function submitMessage(event){
 event.preventDefault();if(sending||startingDraft||!project?.sessionId||stoppingProjects.has(project.id))return;const pid=project.id,gen=epoch,pending=pendingMessage(pid),text=$("#prompt").value.trim();
 if(receiptProblems.has(pid))throw Error(receiptProblems.get(pid));if(pending){await deliverMessage(pid,pending);return;}if(!text)return;if(!await save()||gen!==epoch)return;if(narrationDirty||workflowDirty)throw Error("正文或要求又有修改，请重试发送。");
 const anchor=reviewAnchor?.renderId===$("#output-version").value?`\n\n查看版本：${reviewAnchor.renderId}；定位 ${reviewAnchor.time.toFixed(3)} 秒${reviewAnchor.clipId?"；片段 "+reviewAnchor.clipId:""}。请以所选版本为定位依据，核对当前工程后再修改。`:"";
 const body={text:text+anchor,requestId:id(),purpose:"adjustment",skills:project.workflow?.skills||["video-narration","video-editing","video-review"]};if(session?.turnId&&["running","waiting"].includes(session.status))body.expectedTurnId=session.turnId;rememberMessage(pid,body);await deliverMessage(pid,body);
}
function drawAssets(){
 const box=$("#assets");box.replaceChildren();if(!project.assets?.length)box.append(el("p","还没有素材，也可先从正文开始。","muted small"));
 for(const asset of project.assets||[]){const row=el("div",undefined,"asset"),url="/api/projects/"+project.id+"/assets/"+asset.id;if(asset.mime?.startsWith("image/")){const image=el("img");image.src=url;image.alt=asset.name;image.loading="lazy";row.append(image);}const body=el("div");body.append(el("strong",asset.name));const link=el("a","查看");link.href=url;link.target="_blank";link.rel="noopener";body.append(link);
  if(/\.(txt|md)$/i.test(asset.name)){const use=el("button","作为正文");use.onclick=action(async()=>{const pid=project.id,gen=epoch;const response=await fetch(url);if(!response.ok)throw Error("正文文件暂不可读取");const text=await response.text();if(gen!==epoch||project?.id!==pid)return;if($("#narration-text").value.trim()&&!confirm("用这份文件替换当前正文？本机草稿将更新。"))return;$("#narration-text").value=text;$("#narration-text").dispatchEvent(new Event("input"));});body.append(use);}row.append(body);box.append(row);}
}
async function uploadFiles(files){
 await ensureProject();if(uploading)throw Error("上一批素材仍在导入。");const pid=project.id,gen=epoch;uploading=true;drawWorkflow();
 try{
  const list=Array.from(files),scripts=list.filter(file=>/\.(txt|md)$/i.test(file.name));
  if(scripts.length===1&&!$("#narration-text").value.trim()){if(scripts[0].size>1024*1024)throw Error("正文文件应小于 1 MiB");const text=await scripts[0].text();if(gen!==epoch)return;if(!$("#narration-text").value.trim()){$("#narration-text").value=text;$("#narration-text").dispatchEvent(new Event("input"));$("#narration-status").textContent="已将目录中的唯一文本载入正文草稿。";}}
  let added=0;for(const file of list){if(gen!==epoch)return;if(!/\.(png|jpe?g|webp|mp4|webm|wav|mp3|m4a|flac|ogg|txt|md|pdf)$/i.test(file.name))continue;const body=new FormData();body.append("file",file);const previous=project.revision;let result=await api("/api/projects/"+pid+"/assets",body);if(gen!==epoch)return;if(result.revision<project.revision){added++;continue;}if(gen!==epoch)return;if(result.revision===previous+1)advanceIndependentBases(previous,result.revision);project=result;persistDraft();drawProject();added++;}
  if(gen===epoch)toast(`已导入 ${added} 份素材。`+(scripts.length>1?"有多份文本，请在素材栏选择一份作为正文。":""));
 }finally{if(gen===epoch){uploading=false;drawWorkflow();}}
}
async function loadProductionJobs(){
 if(!project||serviceJobsLoading)return;const pid=project.id,gen=epoch;serviceJobsLoading=true;try{const response=await api("/api/projects/"+pid+"/jobs");if(gen!==epoch)return;const key=JSON.stringify(response.jobs);if(key===serviceJobsKey)return;serviceJobsKey=key;const box=$("#production-jobs");box.replaceChildren();
 const active=activeProductionJobs=(response.jobs||[]).filter(job=>["running","cancel_requested","failed","interrupted","unknown"].includes(job.state));
 for(const job of active){const row=el("div",undefined,"service-job");const label={tts:"配音",render:"视频合成",image_generation:"图像生成",video_generation:"视频生成"}[job.type]||"制作任务";row.append(el("strong",label+" · "+({running:"进行中",cancel_requested:"停止中",failed:"失败",interrupted:"已中断",unknown:"状态待确认"}[job.state]||job.state)));if(job.error)row.append(el("p",job.error,"error small"));box.append(row);}
 drawWorkflow();
 }finally{if(gen===epoch)serviceJobsLoading=false;}
}
async function poll(){
 if(polling||!project||$("#app").hidden)return;polling=true;const gen=epoch,pid=project.id;
 try{const current=await api("/api/projects/"+pid);if(gen!==epoch||current.revision<project.revision)return;if(current.revision!==project.revision&&(narrationDirty||workflowDirty))$("#conflict").hidden=false;project=current;drawProject();
  if(project.sessionId){const [state,events,approvals]=await Promise.all([api("/api/projects/"+pid+"/agent"),api("/api/projects/"+pid+"/events?after="+after),api("/api/projects/"+pid+"/approvals")]);if(gen!==epoch)return;session=state;project.agentSubmission=state.appSubmission;project.agentRunId=state.appRunId;$("#agent-status").textContent=({running:"正在制作",waiting:"等待授权",starting:"启动中",idle:"待命",error:"制作出错",stopped:"已停止",completed:"本次运行结束",interrupted:"已中断",stopping:"停止中"})[state.status]||state.status;reconcileMessage(state.appSubmission);consume(events);drawApprovals(approvals);drawWorkflow();}
  const pending=pendingMessage(pid),next=pendingRecoveryAt.get(pid)||0;if(pending&&!sending&&!startingDraft&&!stoppingProjects.has(pid)&&Date.now()>=next){pendingRecoveryAt.set(pid,Date.now()+10000);try{await deliverMessage(pid,pending);}catch{/* 原请求保留，下一次核对；绝不创建新请求 */}}
 }catch(error){if(gen===epoch)$("#send-state").textContent=error.message;}finally{polling=false;}
}
async function stopProduction(){
 if(!project||stoppingProjects.has(project.id))return;const pid=project.id,gen=epoch,hasSession=!!project.sessionId,pending=project.agentSubmission;
 const expectedRunId=pending?.state==="pending"&&!pending.turnId?"":project.agentRunId||session?.runId||"";
 stoppingProjects.add(pid);$("#agent-stop").disabled=true;drawWorkflow();
 try{
  // Freeze cancellation targets before stopping; a later run must not be swept up.
  const {jobs}=await api("/api/projects/"+pid+"/jobs");const targets=(jobs||[]).filter(job=>["running","cancel_requested"].includes(job.state)).map(job=>job.jobId);
  if(hasSession)await api("/api/projects/"+pid+"/agent/stop",{expectedRunId});
  const failures=[];for(const jobId of targets){try{await api("/api/projects/"+pid+"/jobs/"+encodeURIComponent(jobId)+"/cancel",{});}catch(error){failures.push(error.message);}}
  if(gen===epoch)$("#send-state").textContent=failures.length?"已请求停止，部分任务仍需核对："+failures.join("；"):"已请求停止制作，正在确认运行结果。";if(gen===epoch)await poll();
 }finally{stoppingProjects.delete(pid);if(project?.id===pid){$("#agent-stop").disabled=false;drawWorkflow();}}
}

function eventRow(key,type){let row=rows.get(key);if(row)return row;const box=$("#conversation");if(!rows.size)box.replaceChildren();const node=el("div",undefined,"message "+(type==="user"?"user":type==="agentMessage"?"assistant":"notice"));row={node,item:{type}};rows.set(key,row);box.append(node);return row;}
function drawEvent(row){const item=row.item;if(item.type==="agentMessage"){row.node.textContent=item.text||"";row.node.hidden=!item.text;return;}row.node.textContent=item.text||item.message||item.error?.message||(item.type==="warning"?"运行提示，请在 RunDesk 查看详情。":"运行出现问题，请在 RunDesk 查看详情。");}
function friendlyInput(text){const plain=String(text||"").replace(/^\[Video App[^]*?\n用户要求：\n/,"").trim();return plain.startsWith("为视频项目 "+project.id)?"生成初稿":plain;}
function consume(events){
 const box=$("#conversation"),bottom=box.scrollHeight-box.scrollTop-box.clientHeight<90;
 for(const event of events){after=Math.max(after,event.id);const data=event.data||{},params=data.params||{};
  if(event.method==="run/input"||event.method==="run/steer"){const row=eventRow("input:"+event.id,"user");row.item.text=friendlyInput(data.input?.text);if(event.method==="run/steer")row.item.text="补充："+row.item.text;drawEvent(row);}
  else if(event.direction==="in"&&["item/started","item/completed"].includes(event.method)){const item=params.item;if(!item||item.type!=="agentMessage")continue;const row=eventRow((params.turnId||"")+":"+item.id,"agentMessage");Object.assign(row.item,item);drawEvent(row);}
  else if(event.direction==="in"&&(event.method==="item/agentMessage/delta"||event.method.endsWith("agentMessageDelta"))){const row=eventRow((params.turnId||"")+":"+params.itemId,"agentMessage");row.item.text=(row.item.text||"")+(params.delta||"");drawEvent(row);}
  else if(event.direction==="in"&&["error","warning"].includes(event.method)){const row=eventRow("event:"+event.id,event.method);row.item={type:event.method,message:params.message||params.error?.message};drawEvent(row);}
 }
 if(bottom)box.scrollTop=box.scrollHeight;
}
function drawApprovals(list){const pending=list.filter(item=>item.status==="pending"),key=JSON.stringify(pending.map(item=>item.id));if(key===approvalKey)return;approvalKey=key;$("#approvals").replaceChildren();if(pending.length)$("#approvals").append(el("p","创作助手正在等待授权，请在 RunDesk 查看并处理。","notice small"));}

function drawProductionBoard(board){
 if(board.projectId!==project?.id)throw Error("制作状态不属于当前项目");
 const label={empty:"待制作",running:"进行中",ready:"已有产物",failed:"失败",blocked:"待处理",stale:"需更新"};
 $("#production-board").textContent=(board.stages||[]).map(stage=>stage.title+"："+(label[stage.state]||"状态待确认")).join(" · ");
 const version=board.versionStatus||project.production?.stage||"draft";$("#production-version-status").textContent={draft:"初稿",adjustment:"调整",final:"终稿"}[version]||"初稿";
}
async function loadProductionBoard(force=false){
 if(!project||boardLoading)return;const pid=project.id,gen=epoch;boardLoading=true;
 try{const board=await api("/api/projects/"+pid+"/production-board");if(gen!==epoch||project?.id!==pid)return;const key=JSON.stringify(board);if(force||key!==boardKey){drawProductionBoard(board);boardKey=key;}$("#production-board-status").textContent="详细执行过程可在 RunDesk 查看。";}
 catch(error){if(gen===epoch)$("#production-board-status").textContent="制作状态暂不可用："+error.message;throw error;}finally{if(gen===epoch)boardLoading=false;}
}
async function loadProductionState(force=false){
 if(!project||productionStateLoading&&!force)return;
 const pid=project.id,gen=epoch,serial=++productionStateSerial;productionStateLoading=true;
 try{
  const state=await api("/api/projects/"+pid+"/production-state");
  if(gen!==epoch||project?.id!==pid||serial!==productionStateSerial||state.projectId!==pid)return;
  productionState=state;drawProductionView();drawOutputs(true);
 }catch(error){
  if(gen===epoch&&serial===productionStateSerial){productionState={projectId:pid,state:project?.agentSubmission?.id||pendingMessage(pid)?"unknown":"idle",summary:"制作状态暂不可用："+error.message};drawProductionView();}
 }finally{if(gen===epoch&&serial===productionStateSerial)productionStateLoading=false;}
}
function drawProductionView(){
 const input=$("#production-input"),results=$("#production-results"),outputs=$("#outputs"),progress=$("#production-progress"),phase=$("#production-phase"),logs=$("#production-logs");
 if(!input||!project)return;
 const pending=pendingMessage(project.id),state=productionState?.state||"idle",starting=startingDraft||!!pending,working=starting||["pending","running","unknown"].includes(state)||(!productionState&&!!project.agentSubmission?.purpose),hasLegacy=!!productionState&&!productionState.requestId&&(project.renders||[]).some(render=>render.state==="completed"&&!render.range),complete=state==="completed"&&!!productionState?.renderId;
 input.hidden=page!=="make"||working||(!editingInput&&(complete||state==="no_output"));
 results.hidden=page!=="make"&&page!=="refine"||(!working&&!(["failed","interrupted","canceled","no_output"].includes(state))&&page!=="refine");
 progress.hidden=!working;
 progress.removeAttribute("value");
 phase.textContent=startingDraft?"正在保存并启动制作…":pending?"正在核对原制作请求…":productionState?.summary||({completed:"本次视频已完成",no_output:"本次运行已结束，尚未找到可确认的完整新片",failed:"制作失败",interrupted:"制作已中断",canceled:"制作已停止",idle:"准备制作"}[state]||"正在读取制作状态…");
 logs.replaceChildren();for(const update of (productionState?.updates||[]).slice(-5)){if(update?.summary)logs.append(el("li",update.summary));}
 if(!logs.childNodes.length&&working)logs.append(el("li","正在等待制作动态…"));
 $("#production-board").hidden=!!productionState?.requestId;$("#production-jobs").hidden=!!productionState?.requestId;
 $("#production-retry").hidden=!(["failed","interrupted","canceled","no_output"].includes(state));
 $("#production-edit-input").hidden=page!=="make"||(!complete&&!hasLegacy)||editingInput;
 $("#open-refine-during").hidden=!working||page!=="make";
 $("#go-refine").hidden=page!=="make"||!(complete||hasLegacy)||working;
 $("#make-another").hidden=page!=="refine";
 if(page==="make")outputs.hidden=working||["failed","interrupted","canceled","no_output"].includes(state)||editingInput||(!complete&&!hasLegacy);
 else outputs.hidden=!(project.renders||[]).some(render=>render.state==="completed");
 const preview=productionState?.preview;
 if(working&&preview?.renderId&&preview?.inspectURL)refreshProductionPreview(preview);else{if(previewURL){URL.revokeObjectURL(previewURL);previewURL="";}previewKey="";$("#production-preview").hidden=true;$("#production-preview-status").textContent=working?"本次制作尚无可预览画面。":"";}
 $("#page-status").textContent=page==="refine"?"精修 · "+project.name:"制作 · "+project.name;
}
async function refreshProductionPreview(preview){
 const pid=project.id,gen=epoch,key=JSON.stringify([pid,productionState?.requestId,preview.renderId,preview.time]);if(key===previewKey)return;
 previewKey=key;const match=new RegExp("^/api/projects/"+pid+"/renders/"+preview.renderId+"/inspect$");if(!match.test(preview.inspectURL)||!Number.isFinite(preview.time)||preview.time<0)return;
 try{
  const response=await fetch(preview.inspectURL,{method:"POST",headers:{"Content-Type":"application/json"},body:JSON.stringify({time:preview.time})});if(!response.ok||!response.headers.get("Content-Type")?.startsWith("image/png"))throw Error("画面尚不可用");const blob=await response.blob();if(gen!==epoch||project?.id!==pid||key!==previewKey)return;
  const url=URL.createObjectURL(blob);if(previewURL)URL.revokeObjectURL(previewURL);previewURL=url;$("#production-preview").src=url;$("#production-preview").hidden=false;$("#production-preview-status").textContent="本次成片抽帧 · "+preview.time.toFixed(1)+" 秒";
 }catch{if(gen===epoch&&key===previewKey)$("#production-preview-status").textContent="本次画面预览暂不可用，制作仍在继续。";}
}
let outputChoice="",outputKey="",confirmingFinal=false;
function resetOutputs(){outputChoice="";outputKey="";confirmingFinal=false;delete $("#output-player").dataset.source;$("#output-player").pause();$("#output-player").removeAttribute("src");$("#output-player").hidden=true;$("#output-final-status").textContent="";}
function drawOutputs(force=false){
 if(!project)return;const renders=[...(project.renders||[])].reverse().filter(render=>render.state==="completed"),key=JSON.stringify([renders,project.final,project.production?.finalRenderId,page,productionState?.renderId]);if(!force&&key===outputKey)return;outputKey=key;
 const select=$("#output-version");select.replaceChildren();
 $("#compare-panel").hidden=page!=="refine"||renders.filter(row=>!row.range).length<2;
 if(!renders.length){const option=el("option","制作完成后显示视频");option.value="";select.append(option);outputChoice="";$("#output-player").hidden=true;$("#output-download").hidden=true;$("#output-final").hidden=true;$("#output-status").textContent="视频完成后会出现在这里。";return;}
 for(const render of renders){const final=project.final?.renderId===render.id||project.production?.finalRenderId===render.id;const option=el("option",`${final?"已确认终稿":render.range?"局部预览":"视频版本"} · 修订 ${render.revision} · ${Number(render.duration||0).toFixed(1)} 秒 · ${new Date(render.created).toLocaleString()}`);option.value=render.id;select.append(option);}
 const preferred=page==="make"&&productionState?.renderId;
 if(preferred)outputChoice=preferred;
 else if(!renders.some(render=>render.id===outputChoice))outputChoice=renders.find(render=>!render.range)?.id||renders[0].id;
 select.value=outputChoice;showOutput();
 const compare=$("#compare-version");if(compare){const old=compare.value;compare.replaceChildren(el("option","选择另一个版本"));compare.firstChild.value="";for(const render of renders.filter(row=>!row.range&&row.id!==outputChoice)){const option=el("option",`修订 ${render.revision} · ${Number(render.duration||0).toFixed(1)} 秒`);option.value=render.id;compare.append(option);}compare.value=[...compare.options].some(option=>option.value===old)?old:"";showComparison();}
}
function showOutput(){
 const render=project.renders?.find(item=>item.id===$("#output-version").value&&item.state==="completed");if(!render){$("#output-player").pause();$("#output-player").removeAttribute("src");$("#output-player").hidden=true;$("#output-download").hidden=true;$("#output-final").hidden=true;return;}outputChoice=render.id;const url="/api/projects/"+encodeURIComponent(project.id)+"/renders/"+encodeURIComponent(render.id)+"/final.mp4",player=$("#output-player");if(player.dataset.source!==url){player.pause();player.src=url;player.dataset.source=url;}player.hidden=false;
 $("#output-download").href=url+"?download";$("#output-download").hidden=false;$("#output-download").download="";
 const final=project.final?.renderId===render.id||project.production?.finalRenderId===render.id;$("#output-final").hidden=page!=="refine"||!!render.range||final;$("#output-final").disabled=confirmingFinal;
 $("#output-status").textContent=(render.range?"这是局部预览，不能确认为终稿。":final?"这份完整视频已有人工确认记录。":"这份视频已生成，请观看后提出调整，或确认为终稿。")+(render.revision<project.revision?" 工程已有后续修订，此视频保留原有版本。":"");
 if(page==="refine"&&reviewTimeline?.renderId!==render.id)loadReviewTimeline(render.id).catch(()=>{});
}
function showComparison(){
 const select=$("#compare-version");if(!select)return;
 const render=project?.renders?.find(row=>row.id===select.value&&row.state==="completed"&&!row.range);
 const player=$("#compare-player");if(!render){player.pause();player.removeAttribute("src");player.hidden=true;$("#compare-status").textContent="选择另一个完整版本作对比。";return;}
 const url="/api/projects/"+project.id+"/renders/"+render.id+"/final.mp4";if(player.dataset.source!==url){player.pause();player.src=url;player.dataset.source=url;}player.hidden=false;$("#compare-status").textContent="对比修订 "+render.revision+"；两个播放器分别控制。";
}
async function loadReviewTimeline(renderId){
 if(!project||!renderId||page!=="refine")return;const pid=project.id,gen=epoch;
 try{const timeline=await api("/api/projects/"+pid+"/review-timeline?renderId="+encodeURIComponent(renderId));if(gen!==epoch||project?.id!==pid||$("#output-version").value!==renderId||page!=="refine")return;
  reviewTimeline=timeline;const select=$("#review-clips");select.replaceChildren(el("option","选择镜头或片段"));select.firstChild.value="";
  for(const track of timeline.tracks||[])if(["visual","animation","subtitle"].includes(track.kind))for(const clip of track.clips||[]){const option=el("option",`${track.label||track.kind} · ${clip.label||clip.id} · ${Number(clip.start).toFixed(2)}–${Number(clip.end).toFixed(2)} 秒`);option.value=track.kind+":"+clip.id;option.dataset.time=clip.start;select.append(option);}
  $("#precision-controls").hidden=false;$("#frame-prev").disabled=$("#frame-next").disabled=!(timeline.fps>0);
  $("#review-location-status").textContent=timeline.current?"可选择片段或精确时间点，再用文字说明修改。":"正在查看历史版本；调整前请核对当前工程。";
 }catch(error){if(gen!==epoch)return;reviewTimeline=null;$("#precision-controls").hidden=true;$("#review-location-status").textContent="当前版本的镜头定位暂不可用："+error.message;}
}
function markReviewLocation(){
 const renderId=$("#output-version").value,render=project?.renders?.find(row=>row.id===renderId);if(!render)return;
 const player=$("#output-player"),time=Number(player.currentTime)||0,clip=$("#review-clips").selectedOptions?.[0],clipId=clip?.value||"";
 reviewAnchor={renderId,time,clipId};$("#review-location-status").textContent=`已标记版本 ${renderId.slice(0,8)} 的 ${time.toFixed(3)} 秒${clip?.textContent&&clipId?" · "+clip.textContent:""}。在下方输入修改要求。`;
 $("#prompt").focus();
}
async function confirmOutput(){
 if(confirmingFinal||!project)return;const pid=project.id,gen=epoch,renderId=$("#output-version").value,render=project.renders.find(item=>item.id===renderId);if(!render||render.state!=="completed"||render.range)throw Error("请选择已完成的完整视频。");if(narrationDirty||workflowDirty)throw Error("正文或要求有未提交修改，请先通过对话完成调整，再确认对应视频。");
 confirmingFinal=true;$("#output-final").disabled=true;
 try{await api("/api/projects/"+pid+"/final",{revision:project.revision,renderId,reviewed:true});if(gen!==epoch)return;const refreshed=await api("/api/projects/"+pid);if(gen!==epoch)return;if(refreshed.revision>=project.revision)project=refreshed;outputKey="";drawProject();$("#output-final-status").textContent="已记录你对这份视频的终稿确认。";}
 catch(error){if(gen===epoch)$("#output-final-status").textContent=error.message;throw error;}finally{if(gen===epoch){confirmingFinal=false;$("#output-final").disabled=false;}}
}
$("#login-form").onsubmit=action(async event=>{event.preventDefault();await api("/login",{token:$("#token").value});$("#token").value="";await boot();});
$("#logout").onclick=action(async()=>{persistDraft();await api("/logout",{});location.reload();});
$("#new-project").onclick=action(openNewProject);$("#cancel-new").onclick=closeNewProject;
$("#new-dialog").addEventListener("cancel",()=>{++newDialogSerial;++runtimeCatalogSerial;});
$("#new-form").onsubmit=action(createSelectedProject);
$("#new-profile-search").oninput=renderRuntimeChoices;$("#new-profile").onchange=()=>{selectedRuntimeProfile=$("#new-profile").value;renderRuntimeChoices();};
$("#new-profile-refresh").onclick=action(loadRuntimeProfiles);
$("#project-search").oninput=drawProjectList;
for(const [selector,target]of [["#page-make-link","make"],["#page-refine-link","refine"]])$(selector).onclick=event=>{event.preventDefault();setPage(target);};
window.addEventListener("popstate",()=>{const id=new URL(location.href).searchParams.get("project");if(id&&id!==project?.id){openProject(id,{fromURL:true}).catch(error=>toast(error.message));return;}setPage(getPageFromURL(),true);});
$("#reload-project").onclick=action(async()=>{if(!project||!confirm("载入服务器正文与要求将替换这两项本机草稿，保留对话草稿。继续？"))return;narrationDirty=false;workflowDirty=false;persistDraft();await openProject(project.id);});
$("#narration-text").oninput=()=>{narrationDirty=true;$("#narration-status").textContent="正文草稿已在本机保留，开始制作或发送调整时自动保存。";persistDraft();};
$("#workflow-instructions").oninput=()=>{workflowDirty=true;persistDraft();};$("#prompt").oninput=persistDraft;
$("#narration-import").onclick=()=>$("#narration-file").click();
$("#narration-file").onchange=action(async()=>{const file=$("#narration-file").files[0];if(!file)return;await ensureProject();if(file.size>1024*1024)throw Error("正文文件应小于 1 MiB");const gen=epoch,pid=project.id,text=await file.text();if(gen!==epoch||project?.id!==pid)return;if($("#narration-text").value.trim()&&!confirm("用导入文本替换当前正文？"))return;$("#narration-text").value=text;$("#narration-text").dispatchEvent(new Event("input"));$("#narration-file").value="";});
$("#narration-use-existing").onclick=action(async()=>{const pid=project.id,gen=epoch;let text=project.production?.text;if(!text?.trim()&&project.news){const data=await api("/api/projects/"+pid+"/news");text=data.snapshot?.document?.script_text;}if(gen!==epoch)return;if(!text?.trim())throw Error("没有可读取的已有正文。");if($("#narration-text").value.trim()&&!confirm("替换当前正文草稿？"))return;$("#narration-text").value=text;$("#narration-text").dispatchEvent(new Event("input"));});
$("#upload-button").onclick=()=>$("#upload").click();$("#upload-folder").onclick=()=>$("#folder-files").click();
for(const selector of ["#upload","#folder-files"])$(selector).onchange=action(async()=>{const input=$(selector);const files=Array.from(input.files);try{await uploadFiles(files);}finally{input.value="";}});
$("#import-news").onclick=()=>$("#news-package").click();$("#news-package").onchange=action(async()=>{const file=$("#news-package").files[0];if(!file)return;$("#news-package").value="";if(file.size>4*1024*1024)throw Error("新闻制作包最大 4 MiB");persistDraft();const gen=epoch,body=new FormData();body.append("file",file);$("#import-news").disabled=true;try{const created=await api("/api/news/import",body);if(gen===epoch){const opened=await openProject(created.id);if(opened&&!narrationDirty&&!created.narrationText&&created.production?.text){$("#narration-text").value=created.production.text;$("#narration-text").dispatchEvent(new Event("input"));}}await listProjects();}finally{$("#import-news").disabled=false;}});
$("#draft-start").onclick=action(startProduction);$("#composer").onsubmit=action(submitMessage);$("#agent-stop").onclick=action(stopProduction);
$("#prompt").onkeydown=event=>{if(event.key==="Enter"&&!event.shiftKey&&!event.isComposing){event.preventDefault();$("#composer").requestSubmit();}};
$("#refresh-board").onclick=action(async()=>{await poll();await Promise.all([loadProductionBoard(true),loadProductionState(true)]);});
$("#output-version").onchange=()=>{$("#output-final-status").textContent="";reviewTimeline=null;reviewAnchor=null;showOutput();drawOutputs(true);};$("#output-final").onclick=action(confirmOutput);
$("#go-refine").onclick=()=>setPage("refine");$("#open-refine-during").onclick=event=>{event.preventDefault();setPage("refine");};$("#make-another").onclick=()=>{editingInput=true;setPage("make");};
$("#production-edit-input").onclick=()=>{editingInput=true;drawProductionView();$("#narration-text").focus();};$("#production-retry").onclick=()=>{editingInput=true;drawProductionView();$("#narration-text").focus();};
$("#compare-version").onchange=showComparison;
$("#review-clips").onchange=()=>{const option=$("#review-clips").selectedOptions?.[0];if(option?.dataset.time){$("#seek-time").value=option.dataset.time;$("#seek-apply").click();}};
$("#output-player").addEventListener("timeupdate",()=>{if(page==="refine")$("#seek-time").value=$("#output-player").currentTime.toFixed(3);});
$("#seek-apply").onclick=()=>{const player=$("#output-player"),time=Number($("#seek-time").value),render=project?.renders?.find(row=>row.id===$("#output-version").value);if(!render||!Number.isFinite(time)||time<0||time>=render.duration){toast("请输入当前版本范围内的秒数。");return;}player.currentTime=time;reviewAnchor=null;};
for(const [selector,step]of [["#frame-prev",-1],["#frame-next",1]])$(selector).onclick=()=>{const fps=Number(reviewTimeline?.fps);if(!fps||fps<1)return;const render=project?.renders?.find(row=>row.id===$("#output-version").value);if(!render)return;const player=$("#output-player"),frame=Math.round(player.currentTime*fps)+step;player.currentTime=Math.max(0,Math.min(Math.max(0,render.duration-1/fps),frame/fps));$("#seek-time").value=player.currentTime.toFixed(3);reviewAnchor=null;};
$("#mark-location").onclick=markReviewLocation;
window.addEventListener("beforeunload",persistDraft);document.addEventListener("visibilitychange",()=>{if(document.hidden)persistDraft();});
async function boot(){const list=await listProjects();showApp();const fromURL=new URL(location.href).searchParams.get("project"),selected=list.some(row=>row.id===fromURL)?fromURL:project?.id||list.find(row=>!row.archived)?.id;if(selected)await openProject(selected,{fromURL:!!fromURL&&selected===fromURL});else{setPage("make",true);restoreUnboundDraft();await openNewProject();}if(selected&&(localStorage.getItem(projectCreateKey)||localStorage.getItem(legacyProjectCreateKey)))await openNewProject();api("/api/environment").then(environment=>{rundeskURL=environment.rundeskURL;updateRunDeskLink();setRuntimeConfigLink();}).catch(()=>{});}
boot().catch((e) => { if(e.status!==401){$("#login-error").textContent=e.message;if(!$("#app").hidden)toast(e.message);} });setInterval(poll,1400);
