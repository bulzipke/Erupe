const {test}=require('node:test');
const assert=require('node:assert/strict');
const cal=require('./calendar.js');
const DAY=86400000,KST=9*3600000,start=cal.midnight('2026-10-24');
const clone=value=>JSON.parse(JSON.stringify(value));
function stamp(at){return new Date(at+KST).toISOString().slice(0,19)+'+09:00';}
function document(ids=[53140],from='2026-10-24'){
 const at=cal.midnight(from),profiles=[{id:'left',worlds:['One'],channels:[1]},{id:'right',worlds:['Two'],channels:[]}];
 return {version:'not-live-common-research',coverage:'managed_quests_only',timezone:'Asia/Seoul',from:stamp(at),until:stamp(at+30*DAY),original_entry_conditions_apply:true,
 publication_scope:'all_existing_worlds_and_channels',common_publication:{locations:[{world:'One',channel:1},{world:'Two',channel:1},{world:'Two',channel:2}],profiles:clone(profiles),inventory:ids.map(quest_id=>({quest_id,profiles:['left','right']}))},
 occurrences:ids.map(quest_id=>({quest_id,title:'기존 퀘스트 '+quest_id,schedule_id:'research-only',permanent:true,starts_at:stamp(at),ends_at:stamp(at+30*DAY),profiles:clone(profiles),requirements:['기존 입장권·장비·원래 NPC 진행 조건 유지']}))};
}
test('partial profiles union to common scope; static enabled inventory survives an empty time range',()=>{
 const raw=document();raw.occurrences=[];const m=cal.validateDocument(raw,'2026-10-24',30);
 assert.equal(m.inventoryCount,1);assert.equal(m.locationCount,3);assert.equal(m.rows.length,0);assert.equal(cal.groupDay(m.rows,'2026-10-24','').length,0);
 const loaded=cal.validateDocument(document(),'2026-10-24',30);assert.deepEqual(cal.locationLabels(loaded.rows[0]),['모든 기존 월드·채널 공통']);
 assert.throws(()=>cal.locationLabels({profiles:document().occurrences[0].profiles}));
});
test('fake common, unknown/duplicate/disabled references and incomplete location coverage are rejected',()=>{
 const cases=[
  raw=>delete raw.publication_scope,raw=>delete raw.common_publication,
  raw=>raw.common_publication.locations.push({world:'Missing',channel:1}),
  raw=>raw.common_publication.locations.push({world:'One',channel:1}),
  raw=>raw.common_publication.profiles.push({id:'unused',worlds:['Disabled'],channels:[]}),
  raw=>raw.common_publication.profiles.push(clone(raw.common_publication.profiles[0])),
  raw=>raw.common_publication.inventory.push(clone(raw.common_publication.inventory[0])),
  raw=>raw.common_publication.inventory[0].profiles=['right'],
  raw=>raw.common_publication.inventory[0].profiles=['left','unknown'],
  raw=>raw.common_publication.inventory[0].profiles=['left','right','right'],
  raw=>raw.common_publication.inventory[0].quest_id=0,
  raw=>raw.occurrences[0].quest_id=61067,
  raw=>raw.occurrences[0].profiles=[raw.occurrences[0].profiles[0]],
  raw=>raw.occurrences[0].profiles[0].worlds=['Fake common'],
  raw=>raw.occurrences[0].profiles.push(clone(raw.occurrences[0].profiles[0])),
  raw=>raw.common_publication.locations[0].channel=0,
  raw=>raw.common_publication.profiles[1].channels=[1],
 ];
 for(const change of cases){const raw=document();change(raw);assert.throws(()=>cal.validateDocument(raw,'2026-10-24',30),String(change));}
});
test('metadata and coverage working sets are bounded before expansion',()=>{
 const raw=document();const id='p'.repeat(1024);raw.common_publication.profiles=[{id,worlds:['One','Two']}];raw.common_publication.inventory=Array.from({length:3000},(_,i)=>({quest_id:i+1,profiles:[id]}));raw.occurrences=[];
 assert.throws(()=>cal.validateDocument(raw,'2026-10-24',30),/metadata too large/);
 const combined=document(Array.from({length:1000},(_,i)=>i+1));combined.common_publication.profiles=[{id,worlds:['One','Two']}];combined.common_publication.inventory.forEach(q=>q.profiles=[id]);combined.occurrences.forEach(q=>{q.profiles=[{id,worlds:['One','Two']}];q.title='x'.repeat(1000);});
 assert.throws(()=>cal.validateDocument(combined,'2026-10-24',30),/metadata too large/);
});
class Events{constructor(){this.listeners=new Map();}addEventListener(name,fn){if(!this.listeners.has(name))this.listeners.set(name,new Set());this.listeners.get(name).add(fn);}removeEventListener(name,fn){this.listeners.get(name)?.delete(fn);}dispatch(name){for(const fn of this.listeners.get(name)||[])fn();}}
class Element extends Events{
 constructor(tag,doc){super();this.tagName=tag.toUpperCase();this.ownerDocument=doc;this.children=[];this.dataset={};this.attributes={};this._text='';this.value='';this.classList={add:n=>this.className=n};}
 appendChild(n){this.children.push(n);return n;}replaceChildren(...nodes){this._text='';this.children=nodes;}set textContent(n){this._text=String(n);this.children=[];}get textContent(){return this._text+this.children.map(n=>n.textContent).join('');}set innerHTML(_){throw Error('HTML injection');}setAttribute(k,v){this.attributes[k]=String(v);}
}
function dom(){const doc=new Events();doc.hidden=false;doc.createElement=tag=>new Element(tag,doc);return new Element('main',doc);}
function all(root,predicate){return [root,...root.children.flatMap(n=>all(n,()=>true))].filter(predicate);}
test('calendar DOM has no world picker, collapses receptions, preserves conditions/search/pagination and safe links',async t=>{
 const root=dom(),raw=document(Array.from({length:65},(_,i)=>53140+i));raw.occurrences[0].title='<img src=x onerror=bad()> 原名';
 const widget=cal.mount(root,{now:()=>start+DAY/2,fetch:async()=>new Response(JSON.stringify(raw)),links:{53140:'/quest/ev_hr.htm#lCF94'}});t.after(()=>widget.destroy());await widget.ready;
 assert.equal(all(root,n=>n.tagName==='SELECT').length,0);assert.equal(all(root,n=>n.tagName==='ARTICLE').length,60);assert.equal(all(root,n=>n.tagName==='IMG').length,0);
 assert.match(root.textContent,/모든 기존 월드·채널 공통/);assert.doesNotMatch(root.textContent,/One|Two/);assert.match(root.textContent,/기존 입장권·장비·원래 NPC 진행 조건 유지/);
 const link=all(root,n=>n.tagName==='A')[0];assert.equal(link.href,'https://ferias.bulzipke.com/quest/ev_hr.htm#lCF94');assert.equal(link.rel,'noopener');
 const more=all(root,n=>n.className==='eq-more')[0];more.dispatch('click');assert.equal(all(root,n=>n.tagName==='ARTICLE').length,65);
 const search=all(root,n=>n.tagName==='INPUT')[0];search.value='５３１４０';search.dispatch('input');assert.equal(all(root,n=>n.tagName==='ARTICLE').length,1);assert.match(root.textContent,/당일 공개 1개/);
});
test('visible refresh advances today KST and exactly30days; old failures cannot replace the newest common DOM',async t=>{
 let tick; t.mock.method(globalThis,'setInterval',fn=>{tick=fn;return 5;});t.mock.method(globalThis,'clearInterval',()=>{});
 const root=dom(),calls=[];let now=start+DAY-1;
 const widget=cal.mount(root,{now:()=>now,fetch:async url=>{const from=new URL(url).searchParams.get('from');calls.push(from);return new Response(JSON.stringify(document([53140],from)));}});t.after(()=>widget.destroy());await widget.ready;
 now=start+DAY;tick();await new Promise(resolve=>setImmediate(resolve));assert.deepEqual(calls,['2026-10-24','2026-10-25']);assert.match(root.textContent,/2026-10-25 ~ 2026-11-23/);assert.equal(all(root,n=>(n.className||'').split(' ').includes('eq-day')).length,30);
 let rejectOld,resolveNew;const pending=dom();let count=0;
 const second=cal.mount(pending,{now:()=>start,fetch:()=>++count===1?new Promise((_,reject)=>rejectOld=reject):new Promise(resolve=>resolveNew=resolve)});t.after(()=>second.destroy());
 const newest=second.refresh();resolveNew(new Response(JSON.stringify(document([53141]))));await newest;rejectOld(Error('late old failure'));await second.ready;
 assert.match(pending.textContent,/#53141|# 53141/);assert.doesNotMatch(pending.textContent,/불러오지 못했습니다/);
});
test('unverified raw documents never display a common label or stale roster',async t=>{
 const root=dom(),raw=document();delete raw.publication_scope;
 const widget=cal.mount(root,{now:()=>start,fetch:async()=>new Response(JSON.stringify(raw))});t.after(()=>widget.destroy());await widget.ready;
 assert.match(root.textContent,/불러오지 못했습니다/);assert.doesNotMatch(root.textContent,/모든 기존 월드·채널 공통/);assert.equal(all(root,n=>n.tagName==='ARTICLE').length,0);
});
