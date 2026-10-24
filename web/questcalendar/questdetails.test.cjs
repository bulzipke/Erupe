const {test} = require('node:test');
const assert = require('node:assert/strict');
const details = require('./questdetails.js');
const calendar = require('./calendar.js');
const DAY = 86400000;
const from = calendar.midnight('2026-10-23');
const now = from + 12 * 3600000;

function row(extra = {}) {
  return {quest_id:53140, title:'특별 의뢰', schedule_id:'internal-rotation', starts_at:'2026-10-22T12:00:00+09:00', ends_at:'2026-10-25T12:00:00+09:00',
    profiles:[{id:'internal-events', worlds:['신시어'], channels:[1, 2]}], requirements:['기존 입장권 필요'], ...extra};
}
function model(rows) {
  return calendar.validateDocument({version:'internal-version', coverage:'managed_quests_only', timezone:'Asia/Seoul', from:'2026-10-23T00:00:00+09:00', until:'2026-11-22T00:00:00+09:00',
    publication_scope:'all_existing_worlds_and_channels',common_publication:{locations:[{world:'신시어',channel:1},{world:'신시어',channel:2}],profiles:[{id:'internal-events',worlds:['신시어'],channels:[1,2]}],inventory:[...new Set(rows.map(r=>r.quest_id))].map(quest_id=>({quest_id,profiles:['internal-events']}))},original_entry_conditions_apply:true, occurrences:rows}, '2026-10-23', 30);
}

test('import and anchor parsing do not require a DOM, and only accept bounded Ferias IDs', () => {
  assert.equal(details.parseQuestID('#lCF94'), 53140);
  assert.equal(details.parseQuestID('#lcf94'), 53140);
  assert.equal(details.parseQuestID('#l00000001'), 1);
  assert.equal(details.parseQuestID('#lFFFF'), 65535);
  for (const hash of ['', '#l0', '#l10000', '#l000000001', '#L1', '#l-1', '#l1?x', '#l1/x', '#l%31', '#other', null]) {
    assert.equal(details.parseQuestID(hash), null, String(hash));
  }
});

test('selection includes the start and excludes the end, including a window that started before today', () => {
  const m = model([row()]), q = m.rows[0];
  assert.equal(details.selectStatus(m, q.questID, now).kind, 'open');
  const startsToday = model([row({starts_at:'2026-10-23T12:00:00+09:00'})]);
  assert.equal(details.selectStatus(startsToday, q.questID, now - 1).kind, 'next');
  assert.equal(details.selectStatus(startsToday, q.questID, now).kind, 'open');
  assert.equal(details.selectStatus(m, q.questID, q.end).kind, 'closed');
});

test('only the explicit permanent flag establishes permanent publication', () => {
  const permanent = model([row({permanent:true, schedule_id:'unrelated',starts_at:'2026-10-23T00:00:00+09:00',ends_at:'2026-11-22T00:00:00+09:00'})]);
  assert.equal(details.selectStatus(permanent, 53140, now).kind, 'permanent');
  assert.equal(details.selectStatus(model([row({schedule_id:'always'})]), 53140, now).kind, 'open');
  assert.equal(details.selectStatus(permanent, 53140, permanent.until).kind, 'unavailable');
});

test('the earliest future time retains all common windows without sorting the input', () => {
  const m = model([
    row({starts_at:'2026-10-27T12:00:00+09:00', ends_at:'2026-10-30T12:00:00+09:00'}),
    row({starts_at:'2026-10-24T12:00:00+09:00', ends_at:'2026-10-26T12:00:00+09:00'}),
    row({starts_at:'2026-10-24T12:00:00+09:00', ends_at:'2026-10-25T12:00:00+09:00'})
  ]);
  const before = JSON.stringify(m), status = details.selectStatus(m, 53140, now);
  assert.equal(status.kind, 'next');
  assert.equal(status.nextAt, now + DAY);
  assert.equal(status.next.length, 2);
  assert.deepEqual(status.next.flatMap(row => calendar.locationLabels(row)), ['모든 기존 월드·채널 공통', '모든 기존 월드·채널 공통']);
  assert.equal(JSON.stringify(m), before);
});

test('missing quests and exhausted windows never infer retirement or an unknown next date', () => {
  const m = model([row({ends_at:'2026-10-23T01:00:00+09:00'})]);
  assert.equal(details.selectStatus(m, 53141, now).kind, 'unknown');
  const ended = details.selectStatus(m, 53140, now);
  assert.equal(ended.kind, 'closed'); assert.equal(ended.nextAt, null); assert.deepEqual(ended.next, []);
  assert.equal(details.selectStatus(m, 53140, from - 1).kind, 'unavailable');
  assert.equal(details.selectStatus(m, 53140, m.until).kind, 'unavailable');
  assert.equal(details.selectStatus(m, 0, now).kind, 'unselected');
});

test('next publication is bounded by the shared 30-day document, with no periodic extrapolation', () => {
  const m = model([row({starts_at:'2026-11-21T23:00:00+09:00', ends_at:'2026-11-24T23:00:00+09:00'})]);
  const selected = details.selectStatus(m, 53140, now);
  assert.equal(selected.kind, 'next');
  assert.equal(selected.nextAt, m.until - 3600000);
  assert.equal(details.selectStatus(m, 53140, m.until + DAY).nextAt, null);
});

class Events {
  constructor() { this.listeners = new Map(); }
  addEventListener(name, fn) {
    if (!this.listeners.has(name)) this.listeners.set(name, new Set());
    this.listeners.get(name).add(fn);
  }
  removeEventListener(name, fn) { this.listeners.get(name)?.delete(fn); }
  dispatch(name) { for (const fn of this.listeners.get(name) || []) fn(); }
  listenerCount(name) { return this.listeners.get(name)?.size || 0; }
}
class Element extends Events {
  constructor(tag, doc) {
    super(); this.tagName = tag.toUpperCase(); this.ownerDocument = doc; this.children = []; this.dataset = {}; this.attributes = {};
    this.hidden = false; this.disabled = false; this.className = ''; this.value = '';
    this.classList = {add: name => { this.className += (this.className ? ' ' : '') + name; }};
    this._text = '';
  }
  appendChild(child) { this.children.push(child); return child; }
  replaceChildren(...children) { this._text = ''; this.children = children; }
  set textContent(value) { this._text = String(value); this.children = []; }
  get textContent() { return this._text + this.children.map(child => child.textContent).join(''); }
  set innerHTML(_) { throw new Error('HTML injection is forbidden'); }
  setAttribute(name, value) { this.attributes[name] = String(value); }
}
function dom(hash = '#lCF94') {
  const view = new Events(); view.location = {hash};
  const doc = new Events(); doc.defaultView = view; doc.hidden = false;
  doc.createElement = tag => new Element(tag, doc);
  const container = new Element('section', doc); container.dataset.scheduleUrl = '/test-schedule';
  doc.getElementById = id => id === 'erupe-quest-details' ? container : null;
  return {view, doc, container};
}
function shared(fetchDocument) { return {...calendar, fetchDocument}; }
function tags(element) { return [element.tagName, ...element.children.flatMap(tags)]; }
function options(fetchDocument, extra = {}) { return {now:() => now, calendar:shared(fetchDocument), ...extra}; }

test('mount uses the shared KST 30-day model and renders untrusted strings as text', async t => {
  const d = dom(), calls = [];
  const m = model([row({title:'<img src=x onerror=alert(1)>', requirements:['<script>bad()</script>']}), row({quest_id:53141,title:'다른 퀘스트'})]);
  const widget = details.mount(d.container, options(async (...args) => { calls.push(args); return m; }));
  t.after(() => widget.destroy()); await widget.ready;
  assert.equal(calls.length, 1); assert.equal(calls[0][0], '/test-schedule'); assert.equal(calls[0][1], '2026-10-23'); assert.equal(calls[0][2], 30);
  assert.ok(calls[0][4] instanceof AbortSignal);
  assert.equal(d.container.hidden, false);
  assert.match(d.container.textContent, /<img src=x onerror=alert\(1\)>/);
  assert.match(d.container.textContent, /<script>bad\(\)<\/script>/);
  assert.match(d.container.textContent, /공개 중/);
  assert.match(d.container.textContent, /2026-10-22 12:00 ~ 2026-10-25 12:00 KST/);
  assert.match(d.container.textContent, /모든 기존 월드·채널 공통/);
  assert.match(d.container.textContent, /기존 랭크·장비·입장권·진행 조건이 적용됩니다/);
  assert.match(d.container.textContent, /일정표에 편성된 퀘스트만 집계합니다\. 기존 접수처의 전체 목록은 게임에서 확인하세요\./);
  assert.doesNotMatch(d.container.textContent, /internal-version|internal-rotation|internal-events/);
  assert.ok(!tags(d.container).some(tag => tag === 'IMG' || tag === 'SCRIPT' || tag === 'A'));
  d.view.location.hash = '#lCF95'; d.view.dispatch('hashchange');
  assert.match(d.container.textContent, /다른 퀘스트 #53141/); assert.equal(calls.length, 1);
  d.view.location.hash = '#lCF96'; d.view.dispatch('hashchange');
  assert.match(d.container.textContent, /운영 일정 정보 없음/);
  assert.match(d.container.textContent, /이 정보만으로 퀘스트의 폐지나 배포 여부를 판단할 수 없습니다/);
  assert.doesNotMatch(d.container.textContent, /다른 퀘스트|공개 중/);
});

test('an absent approved legacy quest stays unknown in a managed-only document', async t => {
  const d = dom('#lDA80'), m = model([]);
  assert.equal(details.parseQuestID(d.view.location.hash),55936);
  assert.equal(details.selectStatus(m,55936,now).kind,'unknown');
  const widget = details.mount(d.container, options(async () => m));
  t.after(() => widget.destroy()); await widget.ready;
  assert.match(d.container.textContent, /일정표에 편성된 퀘스트만 집계합니다/);
  assert.match(d.container.textContent, /운영 일정 정보 없음/);
  assert.match(d.container.textContent, /폐지나 배포 여부를 판단할 수 없습니다/);
  assert.doesNotMatch(d.container.textContent, /공개 중|상시 제공|공개 예정|현재 공개 일정 없음/);
});

test('permanent UI never presents the export boundary as a quest expiry', async t => {
  const d = dom(), m = model([row({permanent:true, starts_at:'2026-10-23T00:00:00+09:00', ends_at:'2026-11-22T00:00:00+09:00'})]);
  const widget = details.mount(d.container, options(async () => m));
  t.after(() => widget.destroy()); await widget.ready;
  assert.match(d.container.textContent, /상시 제공/);
  assert.doesNotMatch(d.container.textContent, /2026-11-22|2026-10-23 00:00/);
});

test('refresh clears an old result before loading and after failure', async t => {
  let calls = 0, reject;
  const dependency = shared(async () => ++calls === 1 ? model([row({title:'기존 결과'})]) : new Promise((_, no) => { reject = no; }));
  const other = dom();
  const pendingWidget = details.mount(other.container, {now:() => now, calendar:dependency});
  t.after(() => pendingWidget.destroy()); await pendingWidget.ready;
  const pending = pendingWidget.refresh();
  assert.match(other.container.textContent, /불러오는 중/);
  assert.doesNotMatch(other.container.textContent, /기존 결과|신시어|공개 중/);
  reject(new Error('offline')); await pending;
  assert.match(other.container.textContent, /불러오지 못했습니다/);
  assert.doesNotMatch(other.container.textContent, /기존 결과|신시어|공개 중/);
});

test('root ID lookup, invalid anchors, visibility refresh and destroy clean up state', async () => {
  const d = dom('#not-a-quest'); let calls = 0, resolve;
  const dependency = shared(async () => { calls++; return new Promise(yes => { resolve = yes; }); });
  const widget = details.mount(null, {document:d.doc, now:() => now, calendar:dependency});
  await widget.ready; assert.equal(calls, 0); assert.equal(d.container.hidden, true);
  d.view.location.hash = '#lCF94'; d.view.dispatch('hashchange');
  assert.equal(calls, 1); assert.equal(d.container.hidden, false);
  resolve(model([row()])); await new Promise(setImmediate);
  assert.match(d.container.textContent, /공개 중/);
  d.doc.hidden = true; d.doc.dispatch('visibilitychange'); assert.equal(calls, 1);
  d.doc.hidden = false; d.doc.dispatch('visibilitychange'); assert.equal(calls, 2);
  const late = resolve;
  widget.destroy(); widget.destroy();
  assert.equal(d.view.listenerCount('hashchange'), 0); assert.equal(d.doc.listenerCount('visibilitychange'), 0);
  assert.equal(d.container.textContent, ''); assert.equal(d.container.hidden, true);
  late(model([row({title:'늦은 응답'})])); await new Promise(setImmediate);
  assert.equal(d.container.textContent, '');
  await widget.refresh(); assert.equal(calls, 2);
});

test('the 60-second timer refreshes visible quest details and stops after destroy', async t => {
  t.mock.timers.enable({apis:['setInterval']});
  const d = dom(); let calls = 0;
  const widget = details.mount(d.container, options(async () => { calls++; return model([row()]); }));
  t.after(() => widget.destroy()); await widget.ready;
  t.mock.timers.tick(59999); assert.equal(calls, 1);
  t.mock.timers.tick(1); assert.equal(calls, 2);
  await new Promise(setImmediate);
  d.doc.hidden = true; t.mock.timers.tick(60000); assert.equal(calls, 2);
  widget.destroy(); t.mock.timers.tick(60000); assert.equal(calls, 2);
});
