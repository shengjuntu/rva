const test=require('node:test'),assert=require('node:assert/strict');
const {create}=require('../web/speech-submit.js');
test('render and speech receipts remain isolated and uncertain render retries preserve input',async()=>{
 const items=new Map();const storage={getItem:k=>items.get(k)||null,setItem:(k,v)=>items.set(k,v),removeItem:k=>items.delete(k)};
 const speech=create(()=>storage),render=create(()=>storage,'video-render-pending-v1','渲染');
 const first={type:'render',requestId:'a'.repeat(24),revision:4,options:{kind:'draft'}};
 await assert.rejects(render.submit('project',first,async()=>{throw Error('response lost')}));
 assert.equal(speech.read('project'),null);
 let sent;await render.submit('project',{...first,revision:99},async body=>{sent=body;return{requestId:body.requestId}});
 assert.equal(sent.revision,4);assert.equal(render.read('project'),null);
});
