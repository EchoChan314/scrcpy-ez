'use strict';
const assert = require('assert');
const fs = require('fs');
const vm = require('vm');
const source = fs.readFileSync(__dirname + '/app.js', 'utf8');
const elements = new Map();
function el(id) {
  if (!elements.has(id)) elements.set(id, {textContent:'', style:{}, disabled:false, classList:{add(){},remove(){}}});
  return elements.get(id);
}
const ctx = vm.createContext({el, pairBusy:false, pairCtx:{manual:false,key:'192.0.2.1:45001',name:'old',pairPort:'37000'}});
function loadFunction(name, nextName) {
  const start = source.indexOf('  function ' + name + '(');
  const end = source.indexOf('  function ' + nextName + '(', start);
  assert(start >= 0 && end > start);
  vm.runInContext(source.slice(start,end),ctx);
}
loadFunction('pairAddrPort','pairManualIp');
loadFunction('pairApplyPending','pairSyncFromState');
loadFunction('pairSyncFromState','pairPrefillAddress');
loadFunction('pairRenderFound','pairSetManual');
const pending = [{key:'192.0.2.1:45001',ip:'192.0.2.1',addr:'192.0.2.1:45001',name:'Phone',pairPort:''}];
ctx.pairSyncFromState({pending,pairStatus:{phase:'idle'}});
assert.strictEqual(ctx.pairCtx.pairPort,'','port lost from new snapshot must not remain cached');
ctx.pairRenderFound({phase:'idle'});
assert(el('pair-found-addr').textContent.includes('请打开设备的配对码页面'));
assert(!el('pair-found-addr').textContent.includes('已自动获取'));
pending[0] = {...pending[0],pairPort:'37002'};
ctx.pairSyncFromState({pending,pairStatus:{phase:'idle'}});
assert.strictEqual(ctx.pairCtx.pairPort,'37002','new port must refresh the selected target');
ctx.pairRenderFound({phase:'idle'});
assert(el('pair-found-addr').textContent.includes('配对端口已自动获取'));
ctx.pairBusy=true;
pending[0] = {...pending[0],pairPort:'37003'};
ctx.pairSyncFromState({pending,pairStatus:{phase:'pairing'}});
assert.strictEqual(ctx.pairCtx.pairPort,'37002','active pairing context must remain stable');
ctx.pairBusy=false;
ctx.pairSyncFromState({pending:[],pairStatus:{phase:'idle'}});
ctx.pairRenderFound({phase:'idle'});
assert(el('pair-found-name').textContent.includes('未发现'));
assert.strictEqual(el('pair-go').disabled,true);
console.log('pairing discovery context and port availability passed');
