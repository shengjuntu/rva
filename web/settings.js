"use strict";
const $=selector=>document.querySelector(selector),el=(tag,text,cls)=>{const node=document.createElement(tag);if(text!==undefined)node.textContent=text;if(cls)node.className=cls;return node;};
let ttsSettings=null,settingsDirty=false,savingTTS=false,settingsSerial=0,servicesSerial=0;
async function api(path,body,method){const options={method:method||(body===undefined?"GET":"POST")};if(body!==undefined){options.headers={"Content-Type":"application/json"};options.body=JSON.stringify(body);}const response=await fetch(path,options),data=await response.json();if(!response.ok){const error=new Error(data.error||response.statusText);error.status=response.status;throw error;}return data;}
function toast(message){$("#toast").textContent=message;$("#toast").hidden=false;clearTimeout(toast.timer);toast.timer=setTimeout(()=>$("#toast").hidden=true,6000);}
function action(fn){return async event=>{try{return await fn(event);}catch(error){toast(error.message);}};}
function applyTTSSettings(settings){ttsSettings=settings;for(const [id,key]of [["backend","backend"],["url","url"],["model","model"],["voice","voice"],["language","language"],["timeout","timeoutSeconds"]])$("#tts-"+id).value=settings[key]??"";$("#tts-key").value="";$("#tts-clear-key").checked=false;$("#tts-key").placeholder=settings.hasApiKey?"已保存；留空保持":"服务无认证时可留空";for(const node of $("#tts-form").querySelectorAll("input,select,button"))node.disabled=!!settings.managedByEnvironment;$("#tts-settings-state").textContent=settings.managedByEnvironment?"由环境变量管理，请修改启动配置后重启。":"可在这里保存默认服务，之后制作自动使用。";settingsDirty=false;}
function ttsDraft(){return{backend:$("#tts-backend").value,url:$("#tts-url").value.trim(),model:$("#tts-model").value.trim(),voice:$("#tts-voice").value.trim(),language:$("#tts-language").value,timeoutSeconds:Number($("#tts-timeout").value),apiKey:$("#tts-key").value,clearApiKey:$("#tts-clear-key").checked};}
async function loadEnvironment(){const environment=await api("/api/environment"),box=$("#environment-summary");box.replaceChildren();$("#environment-status").textContent=environment.rundeskReady?"RunDesk 连接正常。":"RunDesk 连接尚未就绪。";const link=$("#settings-rundesk");try{const url=new URL(environment.rundeskURL,location.origin);if(["http:","https:"].includes(url.protocol)){link.href=url.href;link.hidden=false;}}catch{}const rows=[["RunDesk",environment.rundeskReady?"已连接":environment.rundeskError||"待配置"]];const checks=environment.checks||{};for(const [key,name]of [["hyperframes","HyperFrames"],["ffmpeg","FFmpeg"],["font","字体"],["gsap","动画依赖"]])if(checks[key])rows.push([name,checks[key].ready?"已找到依赖":"待配置依赖"]);for(const [name,status]of rows){const row=el("div",undefined,"setting-status");row.append(el("strong",name),el("p",status));box.append(row);}}
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
$("#login-form").onsubmit=action(async event=>{event.preventDefault();await api("/login",{token:$("#token").value});$("#token").value="";await bootSettings();});
$("#tts-form").oninput=()=>{settingsSerial++;settingsDirty=true;$("#tts-settings-state").textContent="有未保存的服务设置。";};
$("#tts-form").onsubmit=action(async event=>{event.preventDefault();if(savingTTS||ttsSettings?.managedByEnvironment)return;savingTTS=true;$("#tts-save").disabled=true;try{const serial=settingsSerial,result=await api("/api/speech/settings",ttsDraft(),"PUT");if(serial===settingsSerial)applyTTSSettings(result);else{ttsSettings=result;settingsDirty=true;$("#tts-settings-state").textContent="原设置已保存，保存期间的新修改仍待提交。";}toast("配音设置已保存。");}finally{savingTTS=false;$("#tts-save").disabled=!!ttsSettings?.managedByEnvironment;}});
$("#tts-discover").onclick=action(async()=>{const button=$("#tts-discover"),url=$("#tts-url").value.trim();button.disabled=true;try{const catalog=await api("/api/speech/voices",ttsSettings?.managedByEnvironment?undefined:ttsDraft());if($("#tts-url").value.trim()!==url)return;$("#tts-voices").replaceChildren();for(const voice of catalog.voices||[]){const option=el("option");option.value=voice;$("#tts-voices").append(option);}$("#speech-environment").textContent=catalog.supported?`读取到 ${catalog.voices?.length||0} 个音色。`:catalog.message||"服务未提供音色列表。";}finally{button.disabled=!!ttsSettings?.managedByEnvironment;}});
$("#speech-check").onclick=action(async()=>{if(settingsDirty)throw Error("请先保存服务设置，再检查连接。");const button=$("#speech-check");button.disabled=true;try{const state=await api("/api/speech/capabilities?backend="+encodeURIComponent(ttsSettings?.backend||"indextts"));$("#speech-environment").textContent=state.ready?"服务连接与基础依赖检查通过；尚未调用真实合成。":state.error||"连接或本机依赖尚未就绪。";}finally{button.disabled=false;}});
$("#refresh-environment").onclick=action(loadEnvironment);$("#refresh-services").onclick=action(async()=>{const button=$("#refresh-services");button.disabled=true;try{await loadServices(true);}finally{button.disabled=false;}});
window.addEventListener("beforeunload",event=>{if(settingsDirty){event.preventDefault();event.returnValue="";}});
async function bootSettings(){applyTTSSettings(await api("/api/speech/settings"));$("#login").hidden=true;$("#settings-app").hidden=false;const results=await Promise.allSettled([loadEnvironment(),loadServices()]);for(const result of results)if(result.status==="rejected")toast(result.reason.message);}
bootSettings().catch((e)=>{if(e.status!==401)$("#login-error").textContent=e.message;});
