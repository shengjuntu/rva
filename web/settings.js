"use strict";
const $=selector=>document.querySelector(selector),el=(tag,text,cls)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node;};
let ttsSettings=null,settingsDirty=false,savingTTS=false,settingsSerial=0,servicesSerial=0,environmentSerial=0;
let managedProjects=[],managedProjectSerial=0,managedProjectsLoading=false,managingProjects=false,pendingProjectDeletion=null;
const managedProjectSelection=new Set();
async function api(path,body,method){const options={method:method||(body===undefined?"GET":"POST")};if(body!==undefined){options.headers={"Content-Type":"application/json"};options.body=JSON.stringify(body);}const response=await fetch(path,options),data=await response.json();if(!response.ok){const error=new Error(data.error||response.statusText);error.status=response.status;throw error;}return data;}
function toast(message){$("#toast").textContent=message;$("#toast").hidden=false;clearTimeout(toast.timer);toast.timer=setTimeout(()=>$("#toast").hidden=true,6000);}
function action(fn){return async event=>{try{return await fn(event);}catch(error){toast(error.message);}};}
function applyTTSSettings(settings){ttsSettings=settings;for(const [id,key]of [["backend","backend"],["url","url"],["model","model"],["voice","voice"],["language","language"],["timeout","timeoutSeconds"]])$("#tts-"+id).value=settings[key]??"";$("#tts-key").value="";$("#tts-clear-key").checked=false;$("#tts-key").placeholder=settings.hasApiKey?"已保存；留空保持":"服务无认证时可留空";for(const node of $("#tts-form").querySelectorAll("input,select,button"))node.disabled=!!settings.managedByEnvironment;$("#tts-settings-state").textContent=settings.managedByEnvironment?"由环境变量管理，请修改启动配置后重启。":"可在这里保存默认服务，之后制作自动使用。";settingsDirty=false;}
function ttsDraft(){return{backend:$("#tts-backend").value,url:$("#tts-url").value.trim(),model:$("#tts-model").value.trim(),voice:$("#tts-voice").value.trim(),language:$("#tts-language").value,timeoutSeconds:Number($("#tts-timeout").value),apiKey:$("#tts-key").value,clearApiKey:$("#tts-clear-key").checked};}
const rundeskBrowserKey="video-rundesk-browser-url";
function browserRunDeskURL(fallback){
 let value="";try{value=localStorage.getItem(rundeskBrowserKey)||"";}catch{}
 if(!value){try{const host=new URL(fallback).hostname;if(["localhost","127.0.0.1","::1","[::1]"].includes(host)&&!["localhost","127.0.0.1","::1","[::1]"].includes(location.hostname))return "";value=fallback;}catch{return "";}}
 try{const url=new URL(value);if(!["http:","https:"].includes(url.protocol)||url.username||url.password||url.search||url.hash)return "";return url.href.replace(/\/$/,"");}catch{return "";}
}
let serverRunDeskURL="";
async function loadEnvironment(){
 const serial=++environmentSerial,results=await Promise.allSettled([api("/api/environment"),api("/api/runtime-profiles")]);if(serial!==environmentSerial)return;
 if(results[0].status!=="fulfilled")throw results[0].reason;
 const environment=results[0].value,catalog=results[1].status==="fulfilled"?results[1].value:null,box=$("#environment-summary");box.replaceChildren();
 $("#environment-status").textContent=environment.rundeskReady?"RunDesk 连接正常。":"RunDesk 连接尚未就绪。";
 serverRunDeskURL=catalog?.rundeskURL||environment.rundeskURL||"";const configured=localStorage.getItem(rundeskBrowserKey)||"";if(document.activeElement!==$("#rundesk-browser-url"))$("#rundesk-browser-url").value=configured;$("#rundesk-browser-status").textContent=!configured&&!browserRunDeskURL(serverRunDeskURL)?"当前服务端地址只对本机有效，请填写从此浏览器可访问的 RunDesk 地址。":configured?"此浏览器已设置访问地址。":"使用服务端地址。";const link=$("#settings-rundesk");link.hidden=true;try{const url=new URL(browserRunDeskURL(serverRunDeskURL));if(["http:","https:"].includes(url.protocol)&&!url.username&&!url.password){link.href=url.href;link.hidden=false;}}catch{}
 const rows=[["RunDesk",environment.rundeskReady?"已连接":environment.rundeskError||"待配置"]];
 if(typeof environment.productionProfile==="string"){const entry=catalog?.items?.find(item=>item.id===environment.productionProfile);rows.push(["新项目默认制作环境",entry?.name||(environment.productionProfile?"已配置的制作环境（名称暂不可读取）":"原默认环境")]);}
 const checks=environment.checks||{};for(const [key,name]of [["hyperframes","HyperFrames"],["ffmpeg","FFmpeg"],["font","字体"],["gsap","动画依赖"]])if(checks[key])rows.push([name,checks[key].ready?"已找到依赖":"待配置依赖"]);
 for(const [name,status]of rows){const row=el("div",undefined,"setting-status");row.append(el("strong",name),el("p",status));box.append(row);}
}
async function loadServices(probe=false){
  const serial=++servicesSerial,response=await api("/api/capabilities"+(probe?"?probe=1":""));
  if(serial!==servicesSerial)return;
  const box=$("#generation-status");box.replaceChildren();
  for(const [id,label]of [["image_generation","图像生成"],["video_generation","视频生成"]]){
    const capability=response.capabilities?.find(item=>item.id===id),provider=capability?.providers?.[0],row=el("div",undefined,"setting-status");
    let status="尚未配置。";
    if(provider?.configured)status=provider.verified==="comfyui_nodes_models"?"ComfyUI 连接、节点与模型列表检查通过；尚未验证真实生成。":"已配置服务，尚未验证真实生成。";
    if(provider?.state==="blocked"&&provider.configured)status="服务检查未通过。";
    row.append(el("strong",label),el("p",status));
    if(provider?.model)row.append(el("p","模型："+provider.model));
    for(const reason of provider?.reasons||[])row.append(el("p",reason));
    box.append(row);
  }
}
function managedProjectBusy(item){return ["running","starting","waiting","stopping","pending","unknown"].includes(item.status);}
function selectedManagedProjects(){return managedProjects.filter(item=>managedProjectSelection.has(item.id));}
function visibleManagedProjects(){const query=$("#managed-project-search").value.trim().toLocaleLowerCase(),filter=$("#managed-project-filter").value;return managedProjects.filter(item=>(filter!=="active"||!item.archived)&&(filter!=="archived"||item.archived)&&item.name.toLocaleLowerCase().includes(query));}
function updateManagedProjectControls(){
  const selected=selectedManagedProjects(),visible=new Set(visibleManagedProjects().map(item=>item.id)),outside=selected.filter(item=>!visible.has(item.id)).length,locked=managedProjectsLoading||managingProjects||!!pendingProjectDeletion;
  $("#managed-project-selection").textContent=selected.length?`已选择 ${selected.length} 个项目${outside?`（当前筛选外 ${outside} 个）`:""}`:"尚未选择项目";
  $("#managed-project-archive").disabled=locked||!selected.some(item=>!item.archived&&!managedProjectBusy(item));
  $("#managed-project-restore").disabled=locked||!selected.some(item=>item.archived);
  $("#managed-project-delete").disabled=locked||!selected.length||selected.some(item=>!item.archived||managedProjectBusy(item));
  $("#managed-project-clear").disabled=locked||!selected.length;
  $("#refresh-managed-projects").disabled=locked;
  for(const node of $("#managed-project-list").querySelectorAll("input,button"))node.disabled=locked;
}
function drawManagedProjects(){
  const box=$("#managed-project-list"),items=visibleManagedProjects();box.replaceChildren();
  if(!items.length)box.append(el("p",managedProjects.length?"没有匹配的项目。":"尚无项目，返回制作页新建。","muted small"));
  for(const item of items){
    const row=el("div",undefined,"managed-project"),label=el("label",undefined,"managed-project-select"),checkbox=el("input");checkbox.type="checkbox";checkbox.checked=managedProjectSelection.has(item.id);checkbox.setAttribute("aria-label","选择项目："+item.name);checkbox.dataset.projectId=item.id;
    checkbox.onchange=()=>{if(checkbox.checked)managedProjectSelection.add(item.id);else managedProjectSelection.delete(item.id);updateManagedProjectControls();};label.append(checkbox);
    const info=el("div",undefined,"managed-project-info"),name=el("a",item.name);name.href="/?project="+encodeURIComponent(item.id)+"&page=make";info.append(name);
    const opened=Date.parse(item.lastOpened||"");info.append(el("p",(item.statusLabel||"状态待确认")+(item.archived?" · 已归档":"")+(Number.isFinite(opened)?" · 最近打开 "+new Date(opened).toLocaleString():""),"muted small"));
    const rename=el("button","重命名");rename.type="button";rename.setAttribute("aria-label","重命名项目："+item.name);rename.onclick=action(async()=>{if(managedProjectsLoading||managingProjects||pendingProjectDeletion)return;const next=window.prompt("项目名称",item.name);if(next===null)return;const name=next.trim();if(!name||name.length>80)throw Error("项目名称需为 1—80 个字符。");if(name===item.name)return;await applyManagedProjectAction("rename",[item],name);});
    row.append(label,info,rename);box.append(row);
  }
  updateManagedProjectControls();
}
async function loadManagedProjects(){
  if(managingProjects||pendingProjectDeletion)return;
  const serial=++managedProjectSerial;managedProjectsLoading=true;updateManagedProjectControls();$("#managed-project-status").textContent="正在读取项目…";
  try{
    const list=await api("/api/projects");if(serial!==managedProjectSerial)return;
    if(!Array.isArray(list)||list.some(item=>!item||typeof item.id!=="string"||!/^[a-f0-9]{24}$/.test(item.id)||typeof item.name!=="string"||!Number.isInteger(item.revision))||new Set(list.map(item=>item.id)).size!==list.length)throw Error("项目列表格式无效，原列表保留。");
    managedProjects=list;const ids=new Set(list.map(item=>item.id));for(const id of managedProjectSelection)if(!ids.has(id))managedProjectSelection.delete(id);
    $("#managed-project-status").textContent=`共 ${list.length} 个项目，${list.filter(item=>item.archived).length} 个已归档。`;drawManagedProjects();
  }catch(error){if(serial===managedProjectSerial)$("#managed-project-status").textContent="项目列表暂不可用："+error.message;throw error;}
  finally{if(serial===managedProjectSerial){managedProjectsLoading=false;updateManagedProjectControls();}}
}
async function applyManagedProjectAction(operation,targets,name){
  if(managingProjects||managedProjectsLoading||!targets.length)return;
  managingProjects=true;++managedProjectSerial;const results=$("#managed-project-results");results.replaceChildren();updateManagedProjectControls();
  const verb={archive:"归档",restore:"恢复",rename:"重命名",delete:"删除"}[operation];let completed=0;
  try{
    for(const item of targets){
      const result=el("p",item.name+"：正在"+verb+"…","management-result small");results.append(result);
      try{
        const route="/api/projects/"+item.id,body=operation==="delete"?{revision:item.revision,confirmName:item.name}:{revision:item.revision,...(operation==="rename"?{name}:{archived:operation==="archive"})};
        const changed=await api(route+(operation==="delete"?"":"/management"),body,operation==="delete"?"DELETE":"PATCH");
        if(operation==="delete"?changed?.ok!==true||changed.id!==item.id:changed?.id!==item.id)throw Error("项目操作回执不匹配，请刷新核对。");
        if(operation==="delete")managedProjects=managedProjects.filter(row=>row.id!==item.id);else managedProjects=managedProjects.map(row=>row.id===item.id?changed:row);
        managedProjectSelection.delete(item.id);completed++;result.textContent=item.name+"："+verb+"成功"+(operation==="delete"?"，已移至服务器回收站。":"。");
      }catch(error){result.textContent=item.name+"："+verb+"失败，"+error.message;result.classList.add("error");}
    }
    $("#managed-project-status").textContent=`${verb}完成：${completed}/${targets.length} 个项目成功。`;drawManagedProjects();
  }finally{managingProjects=false;updateManagedProjectControls();}
  try{await loadManagedProjects();}catch{} // Per-project results stay visible even if refresh fails.
}
function closeProjectDeleteDialog(){pendingProjectDeletion=null;$("#project-delete-dialog").close();$("#project-delete-confirm").value="";updateManagedProjectControls();}
function openProjectDeleteDialog(){
  if(managingProjects||managedProjectsLoading||pendingProjectDeletion)return;
  const selected=selectedManagedProjects();if(!selected.length||selected.some(item=>!item.archived||managedProjectBusy(item)))throw Error("只能删除已归档且未在制作中的项目。");
  pendingProjectDeletion=selected.map(item=>({...item}));const list=$("#project-delete-list");list.replaceChildren();for(const item of pendingProjectDeletion)list.append(el("li",item.name+"（"+item.id.slice(-6)+"）"));
  $("#project-delete-instruction").textContent=`请输入“删除 ${selected.length} 个项目”确认上述项目：`;$("#project-delete-confirm").value="";$("#project-delete-error").textContent="";$("#project-delete-submit").disabled=true;updateManagedProjectControls();$("#project-delete-dialog").showModal();
}
$("#login-form").onsubmit=action(async event=>{event.preventDefault();await api("/login",{token:$("#token").value});$("#token").value="";await bootSettings();});
$("#tts-form").oninput=()=>{settingsSerial++;settingsDirty=true;$("#tts-settings-state").textContent="有未保存的服务设置。";};
$("#tts-form").onsubmit=action(async event=>{event.preventDefault();if(savingTTS||ttsSettings?.managedByEnvironment)return;savingTTS=true;$("#tts-save").disabled=true;try{const serial=settingsSerial,result=await api("/api/speech/settings",ttsDraft(),"PUT");if(serial===settingsSerial)applyTTSSettings(result);else{ttsSettings=result;settingsDirty=true;$("#tts-settings-state").textContent="原设置已保存，保存期间的新修改仍待提交。";}toast("配音设置已保存。");}finally{savingTTS=false;$("#tts-save").disabled=!!ttsSettings?.managedByEnvironment;}});
$("#tts-discover").onclick=action(async()=>{const button=$("#tts-discover"),url=$("#tts-url").value.trim();button.disabled=true;try{const catalog=await api("/api/speech/voices",ttsSettings?.managedByEnvironment?undefined:ttsDraft());if($("#tts-url").value.trim()!==url)return;$("#tts-voices").replaceChildren();for(const voice of catalog.voices||[]){const option=el("option");option.value=voice;$("#tts-voices").append(option);}$("#speech-environment").textContent=catalog.supported?`读取到 ${catalog.voices?.length||0} 个音色。`:catalog.message||"服务未提供音色列表。";}finally{button.disabled=!!ttsSettings?.managedByEnvironment;}});
$("#speech-check").onclick=action(async()=>{if(settingsDirty)throw Error("请先保存服务设置，再检查连接。");const button=$("#speech-check");button.disabled=true;try{const state=await api("/api/speech/capabilities?backend="+encodeURIComponent(ttsSettings?.backend||"indextts"));$("#speech-environment").textContent=state.ready?"服务连接与基础依赖检查通过；尚未调用真实合成。":state.error||"连接或本机依赖尚未就绪。";}finally{button.disabled=false;}});
$("#refresh-environment").onclick=action(loadEnvironment);$("#refresh-services").onclick=action(async()=>{const button=$("#refresh-services");button.disabled=true;try{await loadServices(true);}finally{button.disabled=false;}});
$("#refresh-managed-projects").onclick=action(loadManagedProjects);$("#managed-project-search").oninput=drawManagedProjects;$("#managed-project-filter").onchange=drawManagedProjects;
$("#managed-project-clear").onclick=()=>{if(managingProjects||managedProjectsLoading||pendingProjectDeletion)return;managedProjectSelection.clear();drawManagedProjects();};
$("#managed-project-archive").onclick=action(()=>applyManagedProjectAction("archive",selectedManagedProjects().filter(item=>!item.archived&&!managedProjectBusy(item))));
$("#managed-project-restore").onclick=action(()=>applyManagedProjectAction("restore",selectedManagedProjects().filter(item=>item.archived)));
$("#managed-project-delete").onclick=action(openProjectDeleteDialog);$("#project-delete-cancel").onclick=closeProjectDeleteDialog;$("#project-delete-dialog").addEventListener("cancel",event=>{event.preventDefault();closeProjectDeleteDialog();});
$("#project-delete-confirm").oninput=()=>{$("#project-delete-submit").disabled=!pendingProjectDeletion||$("#project-delete-confirm").value!==`删除 ${pendingProjectDeletion.length} 个项目`;};
$("#project-delete-form").onsubmit=action(async event=>{event.preventDefault();if(!pendingProjectDeletion||$("#project-delete-confirm").value!==`删除 ${pendingProjectDeletion.length} 个项目`)return;const targets=pendingProjectDeletion;closeProjectDeleteDialog();await applyManagedProjectAction("delete",targets);});
$("#rundesk-browser-form").onsubmit=action(async event=>{event.preventDefault();const value=$("#rundesk-browser-url").value.trim(),url=new URL(value);if(!["http:","https:"].includes(url.protocol)||url.username||url.password||url.search||url.hash)throw Error("请输入无账号、查询参数和片段的 HTTP(S) 地址");localStorage.setItem(rundeskBrowserKey,url.href.replace(/\/$/,""));await loadEnvironment();toast("此浏览器的 RunDesk 访问地址已保存。");});
$("#rundesk-browser-clear").onclick=action(async()=>{localStorage.removeItem(rundeskBrowserKey);$("#rundesk-browser-url").value="";await loadEnvironment();});
window.addEventListener("beforeunload",event=>{if(settingsDirty){event.preventDefault();event.returnValue="";}});
async function bootSettings(){applyTTSSettings(await api("/api/speech/settings"));$("#login").hidden=true;$("#settings-app").hidden=false;const results=await Promise.allSettled([loadEnvironment(),loadServices(),loadManagedProjects()]);for(const result of results)if(result.status==="rejected")toast(result.reason.message);}
bootSettings().catch((e)=>{if(e.status!==401)$("#login-error").textContent=e.message;});
