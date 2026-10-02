'use strict';
const assert=require('node:assert/strict'),fs=require('node:fs'),vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'app.js'),'utf8');
function extract(name) {
  const start=source.indexOf('  function '+name+'(');assert.ok(start>=0,name);
  return source.slice(start,source.indexOf('\n  }',start)+4);
}
const addr='192.0.2.12:5555';
const cold={serial:addr,identity:'device:B',appBusy:true};
const warm={serial:'USB_A',identity:'device:A'};
const scope={lastState:{devices:[cold,warm]},sessions:{},appWins:{},appWinModalSerial:null,deviceIdentityOf:serial=>{
  const d=scope.lastState.devices.find(d=>d.serial===serial||d.identity===serial);return d&&d.identity;
}};
vm.createContext(scope);
for(const name of ['iconIdentity','appEntryBusy','setAppEntryMask','syncAppEntryMasks','openAppWin','openAppWinModal']) vm.runInContext(extract(name),scope);
assert.equal(scope.appEntryBusy('device:B'),true);
assert.equal(scope.appEntryBusy('USB_A'),false);
scope.sessions[addr]={identity:'device:A'};
assert.equal(scope.appEntryBusy(addr),false,'an old A session cannot inherit new B address-owner masking');
assert.equal(scope.appEntryBusy('device:B'),true);
function button() {
  let html='<svg>icon</svg>应用窗口',writes=0;
  return {style:{},get innerHTML(){return html;},set innerHTML(v){html=v;writes++;},get writes(){return writes;}};
}
const first=button(),second=button();
scope.appWins['device:B']={pane:{querySelector:()=>first}};
scope.sessions.USB_A={identity:'device:A',pane:{querySelector:()=>second}};
scope.syncAppEntryMasks();assert.equal(first.disabled,true);assert.equal(second.disabled,false);
assert.equal(first.innerHTML,'读取中…');
const writes=first.writes;scope.syncAppEntryMasks();assert.equal(first.writes,writes,'unchanged 700ms snapshots must not rewrite button DOM');
// Both entry dispatchers must refuse a disabled device, even if invoked programmatically.
scope.openAppWin('device:B','B');scope.openAppWinModal('device:B');
assert.equal(scope.appWinModalSerial,null);
let panelRenders=0;
scope.appWinModalSerial='device:B';scope.renderAppWinGrid=()=>{panelRenders++;};
scope.syncAppEntryMasks();assert.equal(scope.appWins['device:B'].initialIconBusy,true);assert.equal(panelRenders,1);
scope.syncAppEntryMasks();assert.equal(panelRenders,1,'unchanged initial mask must not repaint the panel');
cold.appBusy=false;scope.syncAppEntryMasks();
assert.equal(first.disabled,false);assert.equal(first.innerHTML,'<svg>icon</svg>应用窗口');
assert.equal(scope.appWins['device:B'].initialIconBusy,false);assert.equal(panelRenders,2);
console.log('app_entry_mask: both entries, cached device access, fixed release, identity isolation and unchanged DOM passed');
