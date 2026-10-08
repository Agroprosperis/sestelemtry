// Parity harness for internal/dispatch. Everything between the BEGIN and
// END markers is copied verbatim from the approved desk mockup (ems-spec
// fffb893, reference/client_reports/ems_control_mockup/
// ems-dispatch-desk-browser-v2.html); the harness only sets the globals
// it reads. Input: JSON {cases:[...]} on stdin. Output: simulate() rows.
let TARIFFS, PASSPORT, CAP, ETA, MINSOC, MAXSOC, FUTURE_HOURS, PV, PRICE, START;
const fmt=x=>x===null||x===undefined?'—':new Intl.NumberFormat('uk-UA',{maximumFractionDigits:1}).format(x);
const dispatchCache=new Map();
// --- BEGIN mockup ---
function energyPrices(rdn,t=TARIFFS){const vat=t.includeVat?1+t.vatRate:1,margin=t.supplierMode==='pct'?rdn*t.supplierPct/100:t.supplierMargin;return {buy:(rdn+t.distribution+t.transmission+margin+t.otherFees)*vat,sell:rdn*(1-t.exportDiscount)*vat}}
function effectiveExportCap(cfg){return cfg.blockExport?0:(cfg.exportCap??0)}
function solveLP(A,b,c){
 const m=b.length,n=c.length,eps=1e-8,B=Array(m),N=Array(n+1),D=Array.from({length:m+2},()=>new Float64Array(n+2));
 for(let i=0;i<m;i++){for(let j=0;j<n;j++)D[i][j]=A[i][j];B[i]=n+i;D[i][n]=-1;D[i][n+1]=b[i]}
 for(let j=0;j<n;j++){N[j]=j;D[m][j]=-c[j]}N[n]=-1;D[m+1][n]=1;
 function pivot(r,s){
  const inv=1/D[r][s];
  for(let i=0;i<m+2;i++)if(i!==r){const f=D[i][s]*inv;if(f!==0)for(let j=0;j<n+2;j++)if(j!==s)D[i][j]-=D[r][j]*f}
  for(let j=0;j<n+2;j++)if(j!==s)D[r][j]*=inv;
  for(let i=0;i<m+2;i++)if(i!==r)D[i][s]*=-inv;
  D[r][s]=inv;[B[r],N[s]]=[N[s],B[r]];
 }
 function simplex(phase){
  const row=phase===1?m+1:m;
  for(let iteration=0;iteration<10000;iteration++){
   let s=-1;for(let j=0;j<=n;j++){if(phase===2&&N[j]===-1)continue;if(s<0||D[row][j]<D[row][s]-eps||(Math.abs(D[row][j]-D[row][s])<=eps&&N[j]<N[s]))s=j}
   if(s<0||D[row][s]>=-eps)return true;
   let r=-1;for(let i=0;i<m;i++){if(D[i][s]<=eps)continue;const ratio=D[i][n+1]/D[i][s],best=r<0?Infinity:D[r][n+1]/D[r][s];if(r<0||ratio<best-eps||(Math.abs(ratio-best)<=eps&&B[i]<B[r]))r=i}
   if(r<0)return false;pivot(r,s);
  }
  throw new Error('Не вдалося завершити розрахунок демо.');
 }
 let r=0;for(let i=1;i<m;i++)if(D[i][n+1]<D[r][n+1])r=i;
 if(m&&D[r][n+1]<-eps){
  pivot(r,n);if(!simplex(1)||D[m+1][n+1]<-eps||Math.abs(D[m+1][n+1])>eps)return null;
  for(let i=0;i<m;i++)if(B[i]===-1){let s=-1;for(let j=0;j<=n;j++)if(Math.abs(D[i][j])>eps&&(s<0||N[j]<N[s]))s=j;if(s>=0)pivot(i,s)}
 }
 if(!simplex(2))return null;
 const x=new Float64Array(n);for(let i=0;i<m;i++)if(B[i]<n&&B[i]>=0)x[B[i]]=D[i][n+1];return x;
}
function knownLoadHours(model){const i=model.loads.findIndex(v=>!Number.isFinite(v)||v<0);return i<0?FUTURE_HOURS:i}
function optimizeDispatch(model,count=knownLoadHours(model)){
 if(!count)return [];
 const key=JSON.stringify([model,count,PV,PRICE,TARIFFS,START]);if(dispatchCache.has(key))return dispatchCache.get(key);
 const cfg=model.cfg,floor=CAP*Math.max(MINSOC,cfg.reserve)/100,ceiling=CAP*MAXSOC/100,cost=[],rows=[],rhs=[],hours=[],manualGoals=[],overflows=[];
 const variable=weight=>{cost.push(weight);return cost.length-1};
 const limit=(terms,b)=>{rows.push(terms);rhs.push(b)};
 const upper=(v,max)=>limit({[v]:1},Math.max(0,max));
 const equal=(terms,b)=>{limit(terms,b);limit(Object.fromEntries(Object.entries(terms).map(([v,k])=>[v,-k])),-b)};
 const aim=(v,target)=>{upper(v,target);const gap=variable(0);limit({[v]:-1,[gap]:-1},-target);manualGoals.push([gap])};
 const state={};
 for(let i=0;i<count;i++){
  const prices=energyPrices(PRICE[i]),net=model.loads[i]-PV[i],cmd=model.commands[i],charge=variable(0),discharge=variable(-TARIFFS.degradation),buy=variable(-prices.buy),sell=variable(prices.sell),curtail=variable(0),overflow=variable(0);overflows.push(overflow);
  hours.push({charge,discharge,buy,sell,curtail});
  upper(charge,Math.min(PASSPORT.chargeKw,cfg.grid?Math.max(0,cfg.importCap-net):Math.max(0,-net)));
  upper(discharge,Math.min(PASSPORT.dischargeKw,Math.max(0,net+(cfg.export?effectiveExportCap(cfg):0))));
  upper(sell,effectiveExportCap(cfg));upper(curtail,PV[i]);limit({[buy]:1,[overflow]:-1},cfg.importCap);
  equal({[buy]:1,[sell]:-1,[charge]:-1,[discharge]:1,[curtail]:-1},net);
  state[charge]=ETA;state[discharge]=-1/ETA;
  limit({...state},ceiling-START);limit(Object.fromEntries(Object.entries(state).map(([v,k])=>[v,-k])),START-floor);
  if(!cmd)continue;
  if(cmd.s==='hold'){upper(charge,0);upper(discharge,0)}
  else if(cmd.s==='fixed'){const charging=cmd.direction==='charge';upper(charging?discharge:charge,0);aim(charging?charge:discharge,cmd.v)}
  else if(cmd.s==='cover'){upper(charge,0);aim(discharge,Math.min(Math.max(0,net),cmd.v))}
  else if(cmd.s==='solar'){upper(discharge,0);aim(charge,Math.min(Math.max(0,-net),cmd.v))}
  else if(cmd.s==='cap'){const gap=variable(0);manualGoals.push([gap]);limit({[buy]:1,[sell]:-1,[gap]:-1},cmd.v)}
  else if(cmd.s==='export'){
   upper(sell,cmd.v);const gap=variable(0);manualGoals.push([gap]);limit({[buy]:1,[sell]:-1,[gap]:-1},-cmd.v);upper(charge,Math.max(0,-net-cmd.v));upper(discharge,Math.max(0,net+cmd.v));
  }else if(cmd.s==='target'){
   upper(discharge,0);const next=model.commands[i+1];
   if(!next||next.s!=='target'||next.id!==cmd.id){const gap=variable(0),excess=variable(0);manualGoals.push([gap,excess]);limit({...Object.fromEntries(Object.entries(state).map(([v,k])=>[v,-k])),[gap]:-1},START-CAP*cmd.v/100);limit({...state,[excess]:-1},CAP*cmd.v/100-START)}
  }
 }
 // Fix physical feasibility first, then attainable manual goals in time order.
 // A later command cannot trade away an earlier manual goal for a higher RDN price.
 const solve=objective=>{const A=rows.map(terms=>{const row=new Float64Array(cost.length);for(const [v,k] of Object.entries(terms))row[v]=k;return row}),x=solveLP(A,rhs,objective);if(!x)throw new Error('Не вдалося розрахувати план демо.');return x};
 const preserveMinimum=variables=>{const objective=new Float64Array(cost.length);variables.forEach(v=>objective[v]=-1);const x=solve(objective),minimum=variables.reduce((sum,v)=>sum+Math.max(0,x[v]),0);limit(Object.fromEntries(variables.map(v=>[v,1])),minimum+1e-7)};
 preserveMinimum(overflows);
 for(const goal of manualGoals)preserveMinimum(goal);
 // Same SHADOW_SOC as the production forward planner: value remaining DC
 // energy above reserve at the cheapest all-in import over the known horizon.
 // The starting stock/floor constants do not change the maximizing dispatch.
 const shadowPrice=Math.min(...PRICE.slice(0,count).map(rdn=>energyPrices(rdn).buy));
 for(const h of hours){cost[h.charge]+=ETA*shadowPrice;cost[h.discharge]-=shadowPrice/ETA}
 const solution=solve(cost);
 const clean=v=>Math.abs(v)<1e-6?0:Math.max(0,v);
 const plan=hours.map(h=>({charge:clean(solution[h.charge]),discharge:clean(solution[h.discharge]),curtailed:clean(solution[h.curtail])}));
 dispatchCache.set(key,plan);if(dispatchCache.size>12)dispatchCache.delete(dispatchCache.keys().next().value);return plan;
}
function simulate(model){
 let energy=START;const count=knownLoadHours(model);
 const out=[],issues=[],cfg=model.cfg,plan=optimizeDispatch(model,count),exportLimit=effectiveExportCap(cfg);
 for(let i=0;i<FUTURE_HOURS;i++){
  if(i>=count){out.push({p:null,wanted:null,soc:null,before:i===count?energy/CAP*100:null,import:null,export:null,curtailed:null,reasons:[],unknown:true});continue}
  const cmd=model.commands[i],s=cmd?cmd.s:'auto',before=energy,net=model.loads[i]-PV[i],reasons=[],p=plan[i].discharge-plan[i].charge;
  let wanted=p;
  if(s==='cover')wanted=Math.min(Math.max(0,net),cmd.v);
  if(s==='solar')wanted=-Math.min(Math.max(0,-net),cmd.v);
  if(s==='fixed')wanted=(cmd.direction==='charge'?-1:1)*cmd.v;
  if(s==='export')wanted=net+cmd.v;
  const floor=CAP*Math.max(MINSOC,cfg.reserve)/100;
  energy+=plan[i].charge*ETA-plan[i].discharge/ETA;
  const curtailed=plan[i].curtailed,grid=net-p+curtailed,importPower=Math.max(0,grid),exportPower=Math.max(0,-grid);
  const incomplete=['cover','solar','fixed'].includes(s)&&Math.abs(p-wanted)>.05;
  const exportMiss=s==='export'&&exportPower<cmd.v-.05;
  if(incomplete||exportMiss){
   if(wanted>0&&energy<=floor+.05)reasons.push('резерв SOC '+fmt(cfg.reserve)+'%');
   if(wanted<0&&energy>=CAP*MAXSOC/100-.05)reasons.push('максимум SOC '+MAXSOC+'%');
   if(wanted>PASSPORT.dischargeKw+.05||-wanted>PASSPORT.chargeKw+.05)reasons.push('ліміт потужності УЗЕ');
   if(wanted>0&&!cfg.export&&wanted>Math.max(0,net)+.05)reasons.push('продаж енергії УЗЕ вимкнено');
   if(wanted<0&&!cfg.grid&&-wanted>Math.max(0,-net)+.05)reasons.push('бракує надлишку СЕС; заряд із мережі вимкнено');
   if(cfg.blockExport&&wanted>Math.max(0,net)+.05)reasons.push('повна заборона експорту: СЕС та УЗЕ');
   if(s==='export'&&cmd.v>exportLimit+.05&&!cfg.blockExport)reasons.push('ціль експорту перевищує ліміт PCC');
   if(!reasons.length)reasons.push('недостатньо доступної енергії або потужності для всіх ручних команд');
  }
  if(s==='target'){const next=model.commands[i+1];if((!next||next.s!=='target'||next.id!==cmd.id)&&energy<CAP*cmd.v/100-.05)reasons.push('ціль SOC '+fmt(cmd.v)+'% недосяжна')}
  if(importPower>cfg.importCap+.05)reasons.push('імпорт '+fmt(importPower)+' кВт перевищує ліміт '+fmt(cfg.importCap)+' кВт');
  if(s==='cap'&&importPower>cmd.v+.05){if(energy<=floor+.05)reasons.push('резерв SOC '+fmt(cfg.reserve)+'%');if(net-cmd.v>PASSPORT.dischargeKw+.05)reasons.push('ліміт потужності УЗЕ');reasons.push('імпорт PCC '+fmt(importPower)+' кВт перевищує задані '+fmt(cmd.v)+' кВт')}
  if(exportMiss)reasons.push('експорт PCC '+fmt(exportPower)+' із запитаних '+fmt(cmd.v)+' кВт');
  if(reasons.length)issues.push({i,text:[...new Set(reasons)].join('; '),wanted,p,source:s==='auto'?'network':'manual',blocking:true});
  out.push({p,wanted,soc:energy/CAP*100,before:before/CAP*100,import:importPower,export:exportPower,curtailed,reasons});
 }
 return {out,issues,knownHours:count,end:count===FUTURE_HOURS?energy/CAP*100:null,min:count?Math.min(START/CAP*100,...out.slice(0,count).map(x=>x.soc)):null,peak:count?Math.max(...out.slice(0,count).map(x=>x.import)):null};
}
// --- END mockup ---
const input = JSON.parse(require('fs').readFileSync(0, 'utf8'));
const out = input.cases.map(c => {
  TARIFFS = c.tariffs; PASSPORT = c.passport; CAP = PASSPORT.capacityKwh;
  ETA = Math.sqrt(TARIFFS.roundtrip); MINSOC = PASSPORT.socMin; MAXSOC = PASSPORT.socMax;
  FUTURE_HOURS = c.model.loads.length; PV = c.pv; PRICE = c.price; START = c.start;
  dispatchCache.clear();
  const r = simulate(c.model);
  return {known: r.knownHours, hours: r.out.map(h => ({p: h.p, soc: h.soc, import: h.import, export: h.export, curtailed: h.curtailed, unknown: !!h.unknown, reasons: h.reasons}))};
});
process.stdout.write(JSON.stringify(out));
