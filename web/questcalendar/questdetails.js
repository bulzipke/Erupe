(function (root, factory) {
  if (typeof module === 'object' && module.exports) module.exports = factory(() => require('./calendar.js'));
  else root.ErupeQuestDetails = factory(() => root.ErupeQuestCalendar);
})(typeof globalThis === 'object' ? globalThis : this, function (getCalendar) {
  'use strict';

  const KST = 9 * 3600000;

  function parseQuestID(hash) {
    if (typeof hash !== 'string' || !/^#l[0-9a-fA-F]{1,8}$/.test(hash)) return null;
    const id = Number.parseInt(hash.slice(2), 16);
    return id >= 1 && id <= 65535 ? id : null;
  }

  // Consume the calendar's validated, normalized model. Publication does not
  // establish that a player satisfies the quest's original entry conditions.
  function selectStatus(model, questID, now) {
    const empty = { questID, kind: 'unavailable', title: '', active: [], next: [], nextAt: null };
    if (!Number.isInteger(questID) || questID < 1 || questID > 65535) return { ...empty, kind: 'unselected' };
    const at = now instanceof Date ? now.getTime() : Number(now);
    if (!model || !Number.isFinite(at) || at < model.from || at >= model.until) return empty;
    const rows = model.rows.filter(row => row.questID === questID);
    if (!rows.length) return { ...empty, kind: 'unknown' };
    const active = rows.filter(row => row.start <= at && at < row.end);
    let nextAt = null;
    for (const row of rows) {
      if (row.start > at && row.start < model.until && (nextAt === null || row.start < nextAt)) nextAt = row.start;
    }
    const next = nextAt === null ? [] : rows.filter(row => row.start === nextAt);
    const kind = active.some(row => row.permanent === true) ? 'permanent' : active.length ? 'open' : next.length ? 'next' : 'closed';
    const preferred = active.concat(next, rows).find(row => row.title);
    return { questID, kind, title: preferred ? preferred.title : '', active, next, nextAt };
  }

  function formatTime(at) {
    return new Date(at + KST).toISOString().slice(0, 16).replace('T', ' ');
  }

  function mount(container, options) {
    options = options || {};
    const initialDoc = options.document || (typeof document === 'object' ? document : null);
    if (!container && initialDoc) container = initialDoc.getElementById(options.rootID || 'erupe-quest-details');
    if (!container || !container.ownerDocument) throw new Error('quest detail container is required');
    const doc = container.ownerDocument, view = doc.defaultView;
    if (!view || !view.location) throw new Error('quest detail window is required');
    const calendar = options.calendar || getCalendar();
    if (!calendar || typeof calendar.fetchDocument !== 'function' || typeof calendar.dateKey !== 'function' || typeof calendar.locationLabels !== 'function') throw new Error('ErupeQuestCalendar is required');
    const endpoint = options.endpoint || container.dataset.scheduleUrl || '/v2/quests/schedule';
    const clock = options.now || (() => Date.now());
    const fetcher = options.fetch || (typeof view.fetch === 'function' ? view.fetch.bind(view) : null);
    const timeLabel = typeof calendar.formatTime === 'function' ? calendar.formatTime : formatTime;
    let model = null, controller = null, serial = 0, disposed = false;
    function el(tag, className, value) {
      const node = doc.createElement(tag);
      if (className) node.className = className;
      if (value !== undefined) node.textContent = value;
      return node;
    }
    container.classList.add('eq-calendar');
    container.replaceChildren();
    container.appendChild(el('h2', '', '퀘스트 운영 일정'));
    const title = el('h3'); container.appendChild(title);
    const status = el('p', 'eq-status'); status.setAttribute('role', 'status'); container.appendChild(status);
    const content = el('div', 'eq-list'); container.appendChild(content);
    container.appendChild(el('p', 'eq-note', '한국 시간(KST) 기준 · 기존 랭크·장비·입장권·진행 조건이 적용됩니다.'));
    container.appendChild(el('p', 'eq-note', '일정표에 편성된 퀘스트만 집계합니다. 기존 접수처의 전체 목록은 게임에서 확인하세요.'));
    const refreshButton = el('button', '', '새로고침'); refreshButton.type = 'button'; container.appendChild(refreshButton);

    function currentID() { return parseQuestID(view.location.hash); }
    function renderRows(rows) {
      for (const row of rows) {
        const section = el('div', 'eq-quest');
        section.appendChild(el('p', 'eq-window', row.permanent === true ? '상시 제공' : timeLabel(row.start) + ' ~ ' + timeLabel(row.end) + ' KST'));
        // Use the same world/channel labels as the 30-day calendar.
        section.appendChild(el('p', 'eq-location', calendar.locationLabels(row).join(' · ')));
        if (row.requirements.length) section.appendChild(el('p', 'eq-requirements', row.requirements.join(' / ')));
        content.appendChild(section);
      }
    }
    function render(message) {
      const id = currentID();
      container.hidden = id === null;
      title.textContent = ''; status.textContent = ''; status.className = 'eq-status'; content.replaceChildren();
      if (id === null) return;
      title.textContent = '퀘스트 #' + id;
      if (message) { status.textContent = message; return; }
      const selected = selectStatus(model, id, clock());
      if (selected.title) title.textContent = selected.title + ' #' + id;
      if (selected.kind === 'unknown') {
        status.textContent = '운영 일정 정보 없음';
        content.appendChild(el('p', 'eq-note', '이 정보만으로 퀘스트의 폐지나 배포 여부를 판단할 수 없습니다.'));
      } else if (selected.kind === 'unavailable') {
        status.textContent = '일정을 다시 확인해 주세요.';
      } else {
        status.textContent = selected.kind === 'permanent' ? '상시 제공' : selected.kind === 'open' ? '공개 중' : selected.kind === 'next' ? '공개 예정' : '현재 공개 일정 없음';
        if (selected.active.length) {
          status.className = 'eq-status eq-open';
          renderRows(selected.active);
        }
        if (selected.next.length) {
          content.appendChild(el('h4', '', '다음 공개 · ' + timeLabel(selected.nextAt) + ' KST'));
          renderRows(selected.next);
        } else if (!selected.active.length) {
          content.appendChild(el('p', 'eq-note', '조회 범위 안에서 다음 공개 일정이 확인되지 않습니다.'));
        }
      }
    }
    async function refresh() {
      if (disposed) return;
      const current = ++serial;
      if (controller) controller.abort();
      controller = null;
      if (currentID() === null) { render(); return; }
      model = null;
      render('일정을 불러오는 중입니다.');
      refreshButton.disabled = true;
      try {
        controller = new AbortController();
        const next = await calendar.fetchDocument(endpoint, calendar.dateKey(clock()), 30, fetcher, controller.signal);
        if (disposed || current !== serial) return;
        model = next; render();
      } catch (_) {
        if (disposed || current !== serial) return;
        model = null;
        render('일정을 불러오지 못했습니다. 새로고침으로 다시 확인해 주세요.');
      } finally {
        if (!disposed && current === serial) refreshButton.disabled = false;
      }
    }
    function hashChanged() {
      if (disposed) return;
      if (currentID() === null) {
        serial++; if (controller) controller.abort(); controller = null;
        refreshButton.disabled = false; render(); return;
      }
      if (model && calendar.dateKey(model.from) === calendar.dateKey(clock())) render();
      else refresh();
    }
    function visible() { if (!doc.hidden) refresh(); }
    function clicked() { refresh(); }
    view.addEventListener('hashchange', hashChanged);
    doc.addEventListener('visibilitychange', visible);
    refreshButton.addEventListener('click', clicked);
    const timer = setInterval(() => { if (!doc.hidden && currentID() !== null) refresh(); }, 60000);
    const ready = refresh();
    return { ready, refresh, destroy() {
      if (disposed) return;
      disposed = true; serial++; clearInterval(timer);
      if (controller) controller.abort();
      view.removeEventListener('hashchange', hashChanged);
      doc.removeEventListener('visibilitychange', visible);
      refreshButton.removeEventListener('click', clicked);
      model = null; container.replaceChildren(); container.hidden = true;
    } };
  }
  return { parseQuestID, selectStatus, mount };
});
