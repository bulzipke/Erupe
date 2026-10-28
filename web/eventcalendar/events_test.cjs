const assert = require('node:assert/strict');
const {validate, state, format} = require('./events.js');
const phases = [
  {key:'souls',starts_at:'2026-10-26T12:00:00+09:00',ends_at:'2026-11-02T12:00:00+09:00'},
  {key:'tally',starts_at:'2026-11-02T12:00:00+09:00',ends_at:'2026-11-02T14:30:00+09:00'},
  {key:'rewards',starts_at:'2026-11-02T14:30:00+09:00',ends_at:'2026-11-09T12:00:00+09:00'}
];
const festa = {kind:'festa',round:12,mode:'automatic',phases};
const raw = {version:'event-schedule-v1',timezone:'Asia/Seoul',generated_at:'2026-10-28T12:00:00+09:00',events:[festa,{kind:'tower',mode:'unavailable',phases:[]},{kind:'diva',mode:'manual',phases:[]}]};
validate(raw);
assert.equal(state(festa,Date.parse('2026-11-02T11:59:59+09:00')).label,'입혼');
assert.equal(state(festa,Date.parse('2026-11-02T12:00:00+09:00')).label,'집계');
assert.equal(state(festa,Date.parse('2026-11-02T14:30:00+09:00')).label,'보수');
assert.equal(state(festa,Date.parse('2026-11-09T12:00:00+09:00')).label,'회차 갱신 대기');
assert.equal(festa.round,12); // Do not invent the next ID while the worker catches up.
assert.equal(format('2026-11-02T03:00:00Z'),'11/2(월) 12:00');
assert.throws(()=>validate({...raw,timezone:'UTC'}));
assert.throws(()=>validate({...raw,events:[festa,festa,raw.events[2]]}));
assert.throws(()=>validate({...raw,events:[{...festa,phases:[{...phases[0],ends_at:'bad'}]},...raw.events.slice(1)]}));
assert.throws(()=>validate({...raw,events:[{...festa,round:0},...raw.events.slice(1)]}));
console.log('event schedule boundary, timezone, invalid response and stale-round tests passed');
