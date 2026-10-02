'use strict';
const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const source = fs.readFileSync(require('node:path').join(__dirname, 'app.js'), 'utf8');

// Execute the actual renderer against a small DOM, including event handlers and
// rename inputs. This catches failures that pure card helper tests cannot see.
class Element {
  constructor(tag) { this.tagName=tag; this.children=[]; this.style={}; this.dataset={}; this.events={}; this.className=''; this.value=''; }
  get classList() { return {
    contains: c => this.className.split(' ').includes(c),
    add: c => { if (!this.classList.contains(c)) this.className+=' '+c; },
    remove: c => { this.className=this.className.split(' ').filter(x=>x!==c).join(' '); },
    toggle: (c,on) => { (on ? this.classList.add : this.classList.remove)(c); }
  }; }
  set innerHTML(v) { this.children=[]; }
  appendChild(el) { this.children.push(el); el.parentNode=this; return el; }
  addEventListener(name,fn) { this.events[name]=fn; }
  closest(selector) { if(this.classList.contains(selector.slice(1))) return this; return this.parentNode ? this.parentNode.closest(selector) : null; }
  querySelectorAll(selector) { const out=[]; for(const c of this.children) {if(c.classList.contains(selector.slice(1)))out.push(c); out.push(...c.querySelectorAll(selector));} return out; }
}
const nodes={};
const document={getElementById: id => nodes[id] || (nodes[id]=new Element('div')), createElement: tag => new Element(tag),createTextNode: txt => Object.assign(new Element('text'),{textContent:txt}),querySelectorAll: s => nodes['device-list'].querySelectorAll(s.split(' ').pop())};
const scope={document,Date,setTimeout:()=>0,clearTimeout:()=>{},batchMode:false,renameMode:false,batchSel:{},renameDraft:{},batchDeviceKeys:{},lastState:null,appBarLeaving:{},renderedBars:{},deletingKeys:{},devOrder:[],appWins:{},openAppCards:{},fieldSticky:{},
  AppWinBar:require('./appwin_bar'),SessionMap:require('./session_map'),DragOrder:require('./drag_order'),
  sweepDeletingKeys:()=>{},saveOrderBackend:()=>{},scheduleAppBarRefresh:()=>{},profileKeysFor:()=>[],deviceIdentityOf:()=>'',openDeleteBubble:()=>{},startCast:()=>{},switchView:()=>{},openAppWin:()=>{},activateSession:()=>{},switchToDeviceAppWin:()=>{},stopAllAppWins:()=>{},refreshNow:()=>{},StopCast:()=>Promise.resolve(),toast:()=>{},toggleBatchSelect: (key,st)=>{scope.batchSel[key]=!scope.batchSel[key];scope.renderDevices(st);}};
vm.createContext(scope);
for(const name of ['devKey','devDisplayName','el','vtSafeName','fmtSub','stickyKey','stickyFill','batchSelectedDevices','syncBatchUI','collectRenameObj','reconcileBatchDeviceKeys','renderDevices']) {
  const start=source.indexOf('  function '+name+'('); assert.ok(start>=0,name);
  const lineEnd=source.indexOf('\n',start);
  const line=source.slice(start,lineEnd);
  const end=line.trimEnd().endsWith('}') ? lineEnd : source.indexOf('\n  }',lineEnd)+4;
  vm.runInContext(source.slice(start,end),scope);
}
const addr='192.0.2.11:5555';
const pending={serial:addr,identity:'pending:'+addr,identityEpoch:1,state:'device',connType:'wifi',name:'Xiaomi Pad 8 Pro',wirelessIP:addr,wirelessRes:'1920x1280',fps:120,battery:83};
const phone={serial:'192.0.2.12:5555',identity:'device:PHONE',identityEpoch:2,state:'device',connType:'wifi',name:'Xiaomi Pad 8 Pro',stableSerial:'PHONE',wirelessIP:'192.0.2.12:5555',wirelessRes:'1920x864',fps:60};
function render(devices) {scope.lastState={adbOK:true,devices,sessions:[],profiles:[],devOrder:[]};scope.renderDevices(scope.lastState);return nodes['device-list'].children;}
assert.equal(render([pending,phone]).length,2,'ordinary cards');
assert.ok(!scope.fmtSub(phone).includes('序列号'));
assert.ok(scope.fmtSub({serial:'USB_A',state:'device',connType:'usb',res:'3200x2136',fps:120}).includes('USB_A'));
scope.batchMode=true;
let cards=render([pending,phone]);
assert.equal(cards.length,2,'batch mode must render every card without exceptions');
cards[0].events.click({target:cards[0]});
assert.equal(scope.batchSelectedDevices(scope.lastState).length,1);
scope.renameMode=true; cards=render([pending,phone]);
const input=cards[0].querySelectorAll('.rename-input')[0];assert.ok(input,'selected card has name input');
input.value='我的平板';input.events.input();
assert.equal(scope.collectRenameObj(scope.lastState)['pending:'+addr+'|1'],'我的平板','pending rename includes connection token');
const confirmed=Object.assign({},pending,{identity:'device:PAD_A',stableSerial:'PAD_A'});
scope.appWins[addr]={identity:pending.identity,identityEpoch:1};
cards=render([confirmed,phone]);
assert.equal(cards.length,2);
assert.equal(scope.batchSel['device:PAD_A'],true,'selection follows identity confirmation');
assert.equal(cards[0].querySelectorAll('.rename-input')[0].value,'我的平板','draft follows same device');
assert.equal(scope.collectRenameObj(scope.lastState)['device:PAD_A'],'我的平板');
assert.equal(scope.appWins[addr].identity,'device:PAD_A','open application panel follows confirmed archive');
render([Object.assign({},confirmed,{serial:'PAD_A',connType:'usb',identityEpoch:0}),phone]);
assert.equal(scope.batchSel['device:PAD_A'],true,'selection remains on USB switch');
scope.renameMode=false;scope.batchSel={};scope.renameDraft={};render([pending,phone]);
scope.batchSel[pending.identity]=true;scope.renameDraft[pending.identity]='旧名称';
render([Object.assign({},pending,{identityEpoch:3}),phone]);
assert.ok(!scope.batchSel[pending.identity],'a reused IP cannot keep pending selection');
assert.equal(scope.renameDraft[pending.identity],undefined);
scope.batchMode=false;assert.equal(render([confirmed,phone]).length,2,'exit batch mode');
const initialCards=render([Object.assign({},confirmed,{appBusy:true}),phone]);
const firstAppButton=initialCards[0].children.find(c=>c.tagName==='button' && c.textContent==='读取中…');
assert.ok(firstAppButton && firstAppButton.disabled,'only the initial empty-cache app button is disabled');
const warmAppButton=initialCards[1].children.find(c=>c.tagName==='button' && c.textContent==='应用');
assert.ok(warmAppButton && !warmAppButton.disabled,'another device with a cache stays accessible');
console.log('device_cards: normal/batch/rename rendering, identity promotion, transport switch and reused-IP isolation passed');
