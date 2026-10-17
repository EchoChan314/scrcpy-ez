'use strict';
const assert=require('node:assert/strict'), fs=require('node:fs'), vm=require('node:vm');
const source=fs.readFileSync(require('node:path').join(__dirname,'app.js'),'utf8');
function extract(name) { const start=source.indexOf('  function '+name+'('); assert.ok(start>=0); return source.slice(start,source.indexOf('\n  }',start)+4); }
async function settle(){ for(let i=0;i<20;i++) await Promise.resolve(); }
function element(){ return {style:{},dataset:{},children:[],classList:{add(){},remove(){},toggle(){}},setAttribute(){},addEventListener(){},appendChild(n){this.children.push(n);},set innerHTML(v){this.children=[];this.html=v;},get innerHTML(){return this.html||'';}}; }
async function main(){
  const items=Array.from({length:1000},(_,i)=>({pkg:'com.example.app'+i,name:'App '+i}));
  let renders=0,reads=0;
  const timers=[],w={loaded:false,apps:[],identity:'device:A'};
  const scope={appWins:{A:w},deviceIcons:{refresh(){},prune(){}},iconIdentity:()=> 'device:A',
    renderAppWinGrid(){renders++;},invalidateAppIconsForChanges(){},setTimeout:fn=>timers.push(fn),
    window:{CheckAppList:()=>Promise.resolve(true),IsAppListCheckBusy:()=>Promise.resolve({job:1,busy:true,phase:'icons',hasCatalog:true,revision:1,appCount:1000}),GetAppList:()=>{reads++;return Promise.resolve(items);}}};
  vm.createContext(scope);
  for(const name of ['sameAppList','loadAppWinApps','checkAppListSilently']) vm.runInContext(extract(name),scope);
  scope.checkAppListSilently('A');await settle();
  assert.equal(w.apps.length,1000);assert.equal(w.loaded,true);assert.equal(w.listCheckPolling,true);
  assert.equal(reads,1,'list must become usable before icon worker completes');
  timers.shift()();await settle();assert.equal(reads,1);assert.equal(renders,1,'unchanged progress preserves search/scroll DOM');
  scope.window.IsAppListCheckBusy=()=>Promise.resolve({job:1,busy:false,phase:'partial',hasCatalog:true,revision:1,error:'部分图标未完成'});
  timers.shift()();await settle();assert.equal(w.listCheckPolling,false);assert.equal(w.apps.length,1000);
  scope.window.IsAppListCheckBusy=()=>Promise.resolve({job:2,busy:false,phase:'complete',hasCatalog:true,revision:2});
  scope.window.GetAppList=()=>Promise.resolve([]);
  scope.checkAppListSilently('A');await settle();assert.equal(w.loaded,true);assert.equal(w.apps.length,0,'completed empty catalog is not loading');
  // An old cache response cannot replace a newer catalog delivered by status polling.
  let oldResolve;
  scope.window.GetAppList=()=>new Promise(resolve=>{oldResolve=resolve;});
  scope.loadAppWinApps('A');w.listRevision=3;w.apps=items;oldResolve([]);await settle();assert.equal(w.apps.length,1000);
  let startResolve,statusReads=0;
  scope.window.CheckAppList=()=>new Promise(resolve=>{startResolve=resolve;});
  scope.window.IsAppListCheckBusy=()=>{statusReads++;return Promise.resolve({busy:false});};
  scope.checkAppListSilently('A');w.listCheckToken++;w.listCheckPolling=false;startResolve(true);await settle();
  assert.equal(statusReads,0,'closing before start response must cancel polling');
  // Exercise actual rendering: bounded cards per frame and icon reads near viewport.
  const nodes=new Map(),frames=[],observers=[],iconCalls=[];
  scope.el=id=>{if(!nodes.has(id))nodes.set(id,element());return nodes.get(id);};
  Object.assign(scope,{appWinModalSerial:'A',appWinNotificationView:false,notificationSaving:false,
    renderAppReadStatus(){},renderAppNotificationControls(){},notificationPolicyFor:()=>({mode:'off'}),
    appWinVisibleApps:()=>w.apps,appColor:()=> '#123',requestIcon:(serial,pkg)=>iconCalls.push(pkg),
    document:{createElement:element},requestAnimationFrame:fn=>frames.push(fn),
    IntersectionObserver:class {constructor(callback){this.callback=callback;this.targets=[];observers.push(this);}observe(n){this.targets.push(n);}unobserve(){}disconnect(){this.disconnected=true;}}});
  vm.runInContext(extract('renderAppWinGrid'),scope);
  scope.renderAppWinGrid('A');assert.equal(scope.el('appwin-modal-grid').children.length,48);assert.equal(iconCalls.length,0);
  observers[0].callback([{isIntersecting:true,target:observers[0].targets[0]}]);assert.equal(iconCalls.length,1);
  frames.shift()();assert.equal(scope.el('appwin-modal-grid').children.length,96);
  w.apps=items.slice(0,1);scope.renderAppWinGrid('A');assert.equal(observers[0].disconnected,true);
  while(frames.length)frames.shift()();assert.equal(scope.el('appwin-modal-grid').children.length,1,'stale frame cannot append old search results');
  observers[0].callback([{isIntersecting:true,target:observers[0].targets[1]}]);assert.equal(iconCalls.length,1,'stale viewport callbacks ignored');
  // Completion fades the existing text; fresh work cannot be hidden by an old
  // completion callback, and notification editing never shows busy read text.
  const fades=new Map();let timerId=0,reduced=false;
  scope.setTimeout=fn=>{fades.set(++timerId,fn);return timerId;};scope.clearTimeout=id=>fades.delete(id);
  scope.window.matchMedia=()=>({matches:reduced});
  vm.runInContext(extract('renderAppReadStatus'),scope);
  scope.appWinNotificationView=false;
  w.readStatus={busy:true,hasCatalog:true,appCount:161};scope.renderAppReadStatus('A');
  const statusBar=scope.el('appwin-read-status'),message=scope.el('appwin-read-message');
  w.readStatus={phase:'complete'};scope.renderAppReadStatus('A');
  assert.equal(statusBar.style.display,'flex');assert.equal(message.textContent,'应用 161','completion must keep text during fade');
  assert.equal(fades.size,1);const staleFade=[...fades.values()][0];
  scope.renderAppReadStatus('A');assert.equal(fades.size,1,'polling must not restart a fade');
  w.readStatus={busy:true,hasCatalog:true,appCount:162};scope.renderAppReadStatus('A');staleFade();
  assert.equal(statusBar.style.display,'flex');assert.equal(message.textContent,'应用 162','stale fade hid a new read');
  w.readStatus={phase:'complete'};scope.renderAppReadStatus('A');[...fades.values()][0]();
  assert.equal(statusBar.style.display,'none');
  w.readStatus={busy:true};scope.appWinNotificationView=true;scope.renderAppReadStatus('A');
  assert.equal(statusBar.style.display,'none');assert.equal(message.textContent,'');
  w.readStatus={phase:'error',error:'读取中断，可重试'};scope.renderAppReadStatus('A');
  assert.equal(scope.el('appwin-read-retry').style.display,'','notification mode retains actionable errors');
  scope.appWinNotificationView=false;reduced=true;w.readStatus={phase:'complete'};scope.renderAppReadStatus('A');
  assert.equal(statusBar.style.display,'none','reduced-motion users need immediate completion');
  scope.appWinNotificationView=true;w.loaded=false;w.apps=[];w.readStatus={busy:true};
  scope.renderAppWinGrid('A');
  assert.ok(scope.el('appwin-modal-grid').innerHTML.includes('通知来源准备中'));
  assert.ok(!scope.el('appwin-modal-grid').innerHTML.includes('正在读取应用'),'notification loading should use its own context');
  console.log('app_read_pipeline: early catalog, empty success, partial failure, stale responses, per-frame grid and viewport icons passed');
}
main().catch(e=>{console.error(e);process.exitCode=1;});
