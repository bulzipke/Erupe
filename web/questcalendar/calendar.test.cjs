const {test} = require('node:test');
const assert = require('node:assert/strict');
const cal = require('./calendar.js');
const start = cal.midnight('2026-10-23');
function document(rows) {
  return {version:'test-v1',coverage:'managed_quests_only',timezone:'Asia/Seoul',from:'2026-10-23T00:00:00+09:00',until:'2026-11-22T00:00:00+09:00',publication_scope:'all_existing_worlds_and_channels',common_publication:{locations:[{world:'신시어',channel:1}],profiles:[{id:'events',worlds:['신시어'],channels:[1]}],inventory:[...new Set(rows.map(r=>r.quest_id))].map(quest_id=>({quest_id,profiles:['events']}))},original_entry_conditions_apply:true,occurrences:rows};
}
function row(extra={}) {
  return {quest_id:53140,title:'특별 의뢰! 방패게 수렵',schedule_id:'rotation-a',starts_at:'2026-10-22T12:00:00+09:00',ends_at:'2026-10-25T12:00:00+09:00',profiles:[{id:'events',worlds:['신시어'],channels:[1]}],requirements:['기존 입장권 필요'],...extra};
}
test('KST dates and midnight are independent of the host time zone',()=>{
  assert.equal(cal.dateKey(Date.parse('2026-10-22T15:00:00Z')),'2026-10-23');
  assert.equal(cal.dateKey(start-1),'2026-10-22');
  assert.throws(()=>cal.midnight('2026-02-30'));
  assert.throws(()=>cal.midnight('26-10-23'));
});
test('an already started window is included, with end-exclusive days and times',()=>{
  const m = cal.validateDocument(document([row()]),'2026-10-23',30);
  assert.equal(cal.groupDay(m.rows,'2026-10-23','').length,1);
  assert.equal(cal.groupDay(m.rows,'2026-10-25','').length,1);
  assert.equal(cal.groupDay(m.rows,'2026-10-26','').length,0);
  assert.equal(cal.openAt(m.rows[0],m.rows[0].start),true);
  assert.equal(cal.openAt(m.rows[0],m.rows[0].end),false);
  assert.deepEqual(m.rows[0].requirements,['기존 입장권 필요']);
});
test('multiple windows of the same ID count once and retain separate intervals',()=>{
  const m=cal.validateDocument(document([row({ends_at:'2026-10-23T03:00:00+09:00'}),row({starts_at:'2026-10-23T18:00:00+09:00'})]),'2026-10-23',30);
  const groups=cal.groupDay(m.rows,'2026-10-23','');
  assert.equal(groups.length,1);assert.equal(groups[0].windows.length,2);
});
test('permanent flag is explicit and does not infer permanence from a schedule name',()=>{
 const m=cal.validateDocument(document([row({permanent:true,schedule_id:'ordinary-name',starts_at:'2026-10-23T00:00:00+09:00',ends_at:'2026-11-22T00:00:00+09:00'})]),'2026-10-23',30);
 assert.equal(m.rows[0].permanent,true);
 assert.equal(cal.validateDocument(document([row({schedule_id:'always'})]),'2026-10-23',30).rows[0].permanent,false);
 assert.throws(()=>cal.validateDocument(document([row({permanent:'true'})]),'2026-10-23',30));
 assert.throws(()=>cal.validateDocument(document([row({permanent:true})]),'2026-10-23',30));
});
test('malformed dates and normalized 24:00 timestamps are not accepted as real windows',()=>{
 for(const value of ['2026-02-30T12:00:00+09:00','2026-10-23T24:00:00+09:00']) assert.throws(()=>cal.validateDocument(document([row({starts_at:value})]),'2026-10-23',30));
});
test('common location and normalized ID/title search do not change eligibility',()=>{
  const m=cal.validateDocument(document([row()]),'2026-10-23',30), q=m.rows[0];
  assert.equal(cal.groupDay(m.rows,'2026-10-23','５３１４０').length,1);
  assert.equal(cal.matches(q,'방패게'),true);
  assert.equal(cal.matches(q,'없는 제목'),false);
  assert.deepEqual(cal.locationLabels(q),['모든 기존 월드·채널 공통']);
});
test('missing condition disclaimer, stale/short range, invalid IDs and intervals fail',()=>{
  for(const value of [document([row({quest_id:0})]),document([row({ends_at:'2026-10-22T12:00:00+09:00'})]),document([row({profiles:[]})]),{...document([]),original_entry_conditions_apply:false},{...document([]),until:'2026-11-21T00:00:00+09:00'},{...document([]),version:'bad\r\nheader'}]) {
    assert.throws(()=>cal.validateDocument(value,'2026-10-23',30));
  }
  assert.throws(()=>cal.validateDocument(document([]),'2026-10-24',30));
});

test('managed-only scope is required even for an empty calendar',()=>{
  const raw = document([]), m = cal.validateDocument(raw,'2026-10-23',30);
  assert.equal(m.coverage,'managed_quests_only'); assert.deepEqual(m.rows,[]);
  const missing = {...raw}; delete missing.coverage;
  for(const value of [missing,{...raw,coverage:'all_public_quests'},{...raw,coverage:null}]) {
    assert.throws(()=>cal.validateDocument(value,'2026-10-23',30));
  }
});
test('links are restricted to real Ferias quest anchors',()=>{
  assert.equal(cal.detailURL(53140,{'53140':'https://ferias.bulzipke.com/quest/ev_hr.htm#lCF94'}),'https://ferias.bulzipke.com/quest/ev_hr.htm#lCF94');
  for(const bad of ['javascript:alert(1)','https://evil.example/quest/ev_hr.htm#lCF94','https://ferias.bulzipke.com/quest/../../admin#l1','https://ferias.bulzipke.com/quest/ev_hr.htm#other']) assert.equal(cal.detailURL(53140,{'53140':bad}),null);
});
test('fetch forwards the exact requested KST range and validates the response',async()=>{
  let called;
  const got=await cal.fetchDocument('/v2/quests/schedule','2026-10-23',30,async(url,options)=>{
    called={url,options};return new Response(JSON.stringify(document([row()])),{status:200});
  });
  assert.equal(new URL(called.url).searchParams.get('from'),'2026-10-23');
  assert.equal(new URL(called.url).searchParams.get('days'),'30');
  assert.equal(called.options.credentials,'omit');assert.equal(got.rows.length,1);
  await assert.rejects(()=>cal.fetchDocument('/api','2026-10-23',30,async()=>new Response('{}',{status:503})));
  await assert.rejects(()=>cal.fetchDocument('/api','2026-10-23',30,async()=>new Response('{}',{headers:{'Content-Length':String(17*1024*1024)}})));
});

test('mounted calendar explains its scope and cannot fetch or repopulate after destroy',async t=>{
  class Element {
    constructor(doc) { this.ownerDocument=doc; this.children=[]; this.dataset={}; this._text=''; this.classList={add(){}}; }
    appendChild(child) { this.children.push(child); return child; }
    replaceChildren(...children) { this._text=''; this.children=children; }
    set textContent(value) { this._text=String(value); this.children=[]; }
    get textContent() { return this._text+this.children.map(child=>child.textContent).join(''); }
    get options() { return this.children; }
    setAttribute() {} addEventListener() {}
  }
  const doc={hidden:false,createElement(){return new Element(doc);},addEventListener(){},removeEventListener(){}};
  const container=new Element(doc); let calls=0;
  const widget=cal.mount(container,{now:()=>start,fetch:async()=>{calls++;return new Response(JSON.stringify(document([row()])));}});
  t.after(()=>widget.destroy()); await widget.ready;
  assert.equal(calls,1);
  assert.match(container.textContent,/일정표에 편성된 퀘스트만 집계합니다\. 기존 접수처의 전체 목록은 게임에서 확인하세요\./);
  widget.destroy(); await widget.refresh();
  assert.equal(calls,1); assert.equal(container.textContent,''); assert.equal(container.children.length,0);
});
