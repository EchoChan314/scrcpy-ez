'use strict';
const assert=require('node:assert/strict'), fs=require('node:fs'), vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'app.js'),'utf8');
function functionSource(name) {
  const start=source.indexOf('  function '+name+'(');
  return source.slice(start,source.indexOf('\n  }',start)+4);
}
async function settle() { for(let i=0;i<12;i++) await Promise.resolve(); }
async function main() {
  const item={pkg:'com.example.app',name:'App',sys:false};
  let renderCount=0,listReads=0,refreshes=[];
  const w={identity:'device:A',apps:[item],loaded:true};
  const scope={appWins:{A:w},deviceIcons:{refresh:(identity,pkgs)=>{refreshes.push([identity,pkgs]);return Promise.resolve();}},
    iconIdentity:()=> 'device:A',renderAppWinGrid:()=>{renderCount++;},invalidateAppIconsForChanges:()=>{},
    window:{CheckAppList:()=>Promise.resolve(true),IsAppListCheckBusy:()=>Promise.resolve({busy:false,changed:false,icons:['com.example.app']}),GetAppList:()=>{listReads++;return Promise.resolve([item]);}},setTimeout};
  vm.createContext(scope);
  vm.runInContext(functionSource('sameAppList'),scope);
  vm.runInContext(functionSource('checkAppListSilently'),scope);
  scope.checkAppListSilently('A'); await settle();
  assert.equal(renderCount,0,'icon completion must preserve the grid/search/scroll DOM');
  assert.equal(listReads,0,'unchanged list needs no re-fetch');
  assert.equal(refreshes.length,1);assert.equal(refreshes[0][0],'device:A');
  assert.equal(w.listCheckPolling,false);
  // A metadata-only list change also leaves the visible list intact.
  scope.window.IsAppListCheckBusy=()=>Promise.resolve({busy:false,changed:true,icons:['com.example.app']});
  scope.window.GetAppList=()=>Promise.resolve([{...item,iconStamp:'new version'}]);
  scope.checkAppListSilently('A');await settle();assert.equal(renderCount,0);
  // A replaced/closed device panel cannot consume an old asynchronous response.
  let resolve;
  scope.window.IsAppListCheckBusy=()=>new Promise(r=>{resolve=r;});
  scope.checkAppListSilently('A');await settle();
  const previous=refreshes.length;
  scope.appWins.A={identity:'device:B'};
  resolve({busy:false,changed:true,icons:['com.example.app']});await settle();
  assert.equal(refreshes.length,previous);assert.equal(renderCount,0);
  // A race-opened cold panel is covered, then restored on the deadline while the worker is still busy.
  scope.appWins.A=w;
  const timers=[];scope.setTimeout=fn=>{timers.push(fn);};
  const states=[{busy:true,initialIconBusy:true},{busy:true,initialIconBusy:false},{busy:false,changed:false}];
  scope.window.IsAppListCheckBusy=()=>Promise.resolve(states.shift());
  scope.checkAppListSilently('A');await settle();
  assert.equal(w.initialIconBusy,true);assert.equal(renderCount,1);
  timers.shift()();await settle();
  assert.equal(w.initialIconBusy,false);assert.equal(renderCount,2);
  timers.shift()();await settle();
  assert.equal(w.listCheckPolling,false);assert.equal(renderCount,2);
  console.log('app_icon_refresh: completed icons update existing nodes; stale panels and unchanged lists do not repaint');
}
main().catch(e=>{console.error(e);process.exitCode=1;});
