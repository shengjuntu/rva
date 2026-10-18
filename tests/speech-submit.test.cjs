const {test} = require('node:test');
const assert = require('node:assert/strict');
const {create} = require('../web/speech-submit.js');
const body = {revision:3,requestId:'123456789012345678901234',backend:'indextts',voice:'news_zh',speed:1,language:'zh'};
function storage() {
  const data = new Map();
  return {getItem:k=>data.get(k) ?? null,setItem:(k,v)=>data.set(k,v),removeItem:k=>data.delete(k)};
}
test('lost response and tab reload resend the exact request, even if options change', async()=>{
  const disk=storage(), first=create(()=>disk), received=[];
  await assert.rejects(first.submit('p1',body,async request=>{received.push(request);throw new TypeError('network')}));
  const reloaded=create(()=>disk);
  await reloaded.submit('p1',{...body,revision:4,voice:'other',requestId:'987654321098765432109876'},async request=>{
    received.push(request); return {requestId:request.requestId,id:'existing-job'};
  });
  assert.deepEqual(received,[body,body]); assert.equal(reloaded.read('p1'),null);
});
test('polling reconciles only the correct project and request receipt', async()=>{
  const disk=storage(), pending=create(()=>disk);
  await assert.rejects(pending.submit('p1',body,async()=>{throw new Error('timeout')}));
  assert.equal(pending.reconcile('p2',[{requestId:body.requestId}]),null);
  assert.deepEqual(pending.reconcile('p1',[{requestId:'other'}]),body);
  assert.equal(pending.reconcile('p1',[{requestId:body.requestId}]),null);
});
test('uncertain errors and authentication retain request; explicit rejection clears it', async()=>{
  for(const status of [undefined,401,403,408,429,500,502,503,504,400,404,409,422]) {
    const disk=storage(), s=create(()=>disk);
    await assert.rejects(s.submit('p',body,async()=>{throw Object.assign(new Error('failed'),{status})}));
    assert.equal(s.read('p') === null,[400,404,409,422].includes(status),String(status));
  }
});
test('unmatched response cannot acknowledge an uncertain request', async()=>{
  const disk=storage(), s=create(()=>disk);
  await assert.rejects(s.submit('p',body,async()=>({requestId:'wrong'})));
  assert.deepEqual(s.read('p'),body);
});
test('disabled storage never sends the request', async()=>{
  let sent=false;
  const disk=storage(); disk.setItem=()=>{throw new Error('quota')};
  await assert.rejects(create(()=>disk).submit('p',body,async()=>{sent=true}));
  assert.equal(sent,false);
  assert.throws(()=>create(()=>{throw new Error('denied')}).read('p'));
});
