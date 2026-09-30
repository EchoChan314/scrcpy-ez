const assert = require('node:assert/strict');
const fs = require('node:fs');
const vm = require('node:vm');
const ui = require('./update_ui');
const elements = new Map();
function el(id) {
  if (!elements.has(id)) elements.set(id,{style:{display:'none'},classList:{add(){},remove(){}},listeners:{},addEventListener(n,f){this.listeners[n]=f;},removeAttribute(){},textContent:''});
  return elements.get(id);
}
const calls=[]; let current={phase:'available',info:{current:'v2.2.6',latest:'v2.3.0',hasNew:true}};
const window = {SCEZUpdateUI:ui,GetUpdateState:()=>Promise.resolve(current),BeginUpdateCheck:()=>{calls.push('check');return Promise.resolve(current);},DownloadUpdate:()=>{calls.push('download');current={...current,phase:'downloading',message:'正在下载'};return Promise.resolve();},CancelUpdate:()=>{calls.push('cancel');return Promise.resolve();},InstallUpdate:(confirm)=>{calls.push(confirm?'confirm-install':'install');return Promise.resolve(confirm?{}:{needsConfirm:true,activeWindows:2});},DismissUpdateResult:()=>Promise.resolve(),OpenURL:()=>Promise.resolve()};
const code=fs.readFileSync(__dirname+'/app.js','utf8');
const start=code.indexOf('  // ---------- 应用内更新：');
const end=code.indexOf('  // ---------- 参数状态机',start);
const context=vm.createContext({window,el,lastState:null,toast(){},setTimeout(){},setInterval(){}});
vm.runInContext(code.slice(start,end),context);
const tick=()=>new Promise(r=>setImmediate(r));
(async()=>{
  el('app-ver').listeners.click();await tick();await tick();
  assert.equal(el('update-download').style.display,'');
  el('update-download').listeners.click();await tick();await tick();
  assert.equal(el('update-cancel').style.display,'');
  el('update-close').listeners.click();assert.equal(el('update-modal').style.display,'none');assert.equal(calls.includes('cancel'),false);
  current={...current,phase:'ready',canInstall:true};el('app-ver').listeners.click();await tick();await tick();
  el('update-install').listeners.click();await tick();await tick();
  assert.equal(el('update-confirm').style.display,'');assert.match(el('update-confirm-text').textContent,/2 个投屏窗口/);
  el('update-confirm-cancel').listeners.click();assert.equal(calls.includes('confirm-install'),false);
  el('app-ver').listeners.click();await tick();await tick();el('update-install').listeners.click();await tick();await tick();el('update-confirm-ok').listeners.click();await tick();await tick();
  assert.equal(calls.includes('confirm-install'),true);
  console.log('update modal download, close, active casts and explicit install cases passed');
})().catch(e=>{console.error(e);process.exitCode=1;});
