(function (root, factory) {
  'use strict';
  if (typeof module === 'object' && module.exports) module.exports = factory();
  else root.ErupeEvents = factory();
}(this, function () {
  'use strict';
  var names = {festa: '헌터축제', tower: '천랑원정록', diva: '가희수위전'};
  var labels = {registration: '등록', souls: '입혼', tally: '집계', rewards: '보수', exploration: '진행', prayer: '기도의 장', battle: '전가의 장', welcome: '가영의 장'};
  function format(value) {
    var d = new Date(Date.parse(value) + 9 * 3600000);
    function pad(n) { return n < 10 ? '0' + n : String(n); }
    return (d.getUTCMonth() + 1) + '/' + d.getUTCDate() + '(' + '일월화수목금토'.charAt(d.getUTCDay()) + ') ' + pad(d.getUTCHours()) + ':' + pad(d.getUTCMinutes());
  }
  function validate(raw) {
    if (!raw || raw.version !== 'event-schedule-v1' || raw.timezone !== 'Asia/Seoul' || !isFinite(Date.parse(raw.generated_at)) || !Array.isArray(raw.events) || raw.events.length !== 3) throw new Error('invalid schedule');
    var seen = {};
    raw.events.forEach(function (e) {
      if (!Object.prototype.hasOwnProperty.call(names, e.kind) || seen[e.kind] || !Array.isArray(e.phases) || ['automatic', 'manual', 'unavailable'].indexOf(e.mode) < 0) throw new Error('invalid event');
      seen[e.kind] = true;
      if (e.mode === 'automatic' && (typeof e.round !== 'number' || Math.floor(e.round) !== e.round || e.round <= 0 || e.phases.length === 0 || e.phases.length > 4)) throw new Error('invalid round');
      var previous = -Infinity;
      e.phases.forEach(function (p) {
        var start = Date.parse(p.starts_at), end = Date.parse(p.ends_at);
        if (!Object.prototype.hasOwnProperty.call(labels, p.key) || !isFinite(start) || !isFinite(end) || start >= end || start < previous) throw new Error('invalid phase');
        previous = end;
      });
      if (e.claim_until && !isFinite(Date.parse(e.claim_until))) throw new Error('invalid claim date');
    });
    return raw;
  }
  function state(e, now) {
    if (e.mode !== 'automatic') return {label: e.mode === 'manual' ? '자동 일정 없음' : '개최 정보 없음'};
    for (var i = 0; i < e.phases.length; i++) {
      var p = e.phases[i];
      if (now < Date.parse(p.starts_at)) return {label: '시작 예정', next: p.starts_at, nextLabel: labels[p.key]};
      if (now < Date.parse(p.ends_at)) return {label: labels[p.key], phase: p, next: p.ends_at, nextLabel: e.phases[i + 1] ? labels[e.phases[i + 1].key] : '새 회차'};
    }
    return {label: '회차 갱신 대기'};
  }
  function mount(container) {
    var doc = container.ownerDocument, model = null, offset = 0, request = null, destroyed = false, rendered = '';
    function el(tag, text, cls) { var node = doc.createElement(tag); if (text !== undefined) node.textContent = text; if (cls) node.className = cls; return node; }
    var message = el('p', '서버 일정을 불러오는 중입니다.', 'erupe-events-message');
    var content = el('div', undefined, 'erupe-events-content');
    container.appendChild(message); container.appendChild(content);
    function render() {
      if (!model || destroyed) return;
      var signature = JSON.stringify(model.events) + model.events.map(function (e) { return state(e, Date.now() + offset).label; }).join('|');
      if (signature === rendered) return;
      rendered = signature;
      while (content.firstChild) content.removeChild(content.firstChild);
      var table = el('table', undefined, 'erupe-events-table'), head = el('tr'), body = el('tbody');
      ['콘텐츠', '현재 단계', '진행 기간', '다음 전환'].forEach(function (t) { head.appendChild(el('th', t)); });
      var thead = el('thead'); thead.appendChild(head); table.appendChild(thead); table.appendChild(body);
      model.events.forEach(function (e) {
        var tr = el('tr'), s = state(e, Date.now() + offset);
        tr.appendChild(el('th', names[e.kind] + (e.round ? ' ' + e.round + '회' : '')));
        tr.appendChild(el('td', s.label, 'erupe-events-phase'));
        tr.appendChild(el('td', s.phase ? format(s.phase.starts_at) + ' ~ ' + format(s.phase.ends_at) : '-'));
        tr.appendChild(el('td', s.next ? format(s.next) + ' ' + s.nextLabel : '-')); body.appendChild(tr);
      });
      content.appendChild(table);
      var details = el('details'), summary = el('summary', '단계별 전체 일정'); details.appendChild(summary);
      model.events.forEach(function (e) {
        if (!e.phases.length) return;
        var block = el('div', undefined, 'erupe-events-detail');
        block.appendChild(el('strong', names[e.kind] + ' ' + e.round + '회'));
        e.phases.forEach(function (p) { block.appendChild(el('div', labels[p.key] + ': ' + format(p.starts_at) + ' ~ ' + format(p.ends_at))); });
        details.appendChild(block);
      });
      content.appendChild(details);
    }
    function refresh() {
      if (destroyed || request) return;
      request = new XMLHttpRequest(); request.open('GET', container.getAttribute('data-schedule-url') || '/v2/events/schedule', true); request.timeout = 8000;
      request.onload = function () {
        if (destroyed) return;
        try {
          if (request.status !== 200) throw new Error('unavailable');
          var next = validate(JSON.parse(request.responseText));
          model = next; offset = Date.parse(next.generated_at) - Date.now();
          message.hidden = true; render();
        } catch (_) { fail(); }
        request = null;
      };
      function fail() {
        // Keep an old response visibly stale instead of presenting it as live.
        model = null; rendered = ''; while (content.firstChild) content.removeChild(content.firstChild);
        message.hidden = false;
        message.textContent = '서버 일정을 불러오지 못했습니다. 잠시 후 자동으로 다시 확인합니다.'; request = null;
      }
      request.onerror = fail; request.ontimeout = fail; request.send();
    }
    var poll = setInterval(refresh, 30000), tick = setInterval(render, 1000);
    refresh();
    function destroy() { destroyed = true; clearInterval(poll); clearInterval(tick); if (request) request.abort(); }
    window.addEventListener('pagehide', destroy);
    return {destroy: destroy};
  }
  return {validate: validate, state: state, format: format, mount: mount};
}));
