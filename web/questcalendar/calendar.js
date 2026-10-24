(function (root, factory) {
  const api = factory();
  if (typeof module === 'object' && module.exports) module.exports = api;
  else root.ErupeQuestCalendar = api;
})(typeof globalThis === 'object' ? globalThis : this, function () {
  'use strict';
  const DAY = 86400000, KST = 9 * 3600000;
  const MAX_ROWS = 100000, MAX_BYTES = 16 * 1024 * 1024;
  function dateKey(value) {
    const n = value instanceof Date ? value.getTime() : Number(value);
    if (!Number.isFinite(n)) throw new Error('invalid time');
    return new Date(n + KST).toISOString().slice(0, 10);
  }
  function midnight(key) {
    if (!/^\d{4}-\d{2}-\d{2}$/.test(key)) throw new Error('invalid date');
    const n = Date.parse(key + 'T00:00:00+09:00');
    if (!Number.isFinite(n) || dateKey(n) !== key) throw new Error('invalid date');
    return n;
  }
  function timestamp(value) {
    if (typeof value !== 'string' || !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:Z|[+-]\d{2}:\d{2})$/.test(value)) throw new Error('invalid timestamp');
    const n = Date.parse(value);
    if (!Number.isFinite(n)) throw new Error('invalid timestamp');
    const parts = value.slice(0, 19).split(/[-T:]/).map(Number);
    const wall = new Date(0);
    wall.setUTCFullYear(parts[0], parts[1] - 1, parts[2]);
    wall.setUTCHours(parts[3], parts[4], parts[5], 0);
    if (parts[0] < 1 || wall.getUTCFullYear() !== parts[0] || wall.getUTCMonth() + 1 !== parts[1] || wall.getUTCDate() !== parts[2] || wall.getUTCHours() !== parts[3] || wall.getUTCMinutes() !== parts[4] || wall.getUTCSeconds() !== parts[5]) throw new Error('invalid timestamp');
    return n;
  }
  function text(value, limit) {
    if (typeof value !== 'string' || value.length > limit) throw new Error('invalid text');
    return value;
  }
  function validateCommonPublication(raw) {
    const proof = raw.common_publication;
    if (raw.publication_scope !== 'all_existing_worlds_and_channels' || !proof || !Array.isArray(proof.locations) || !proof.locations.length || proof.locations.length > 65535 || !Array.isArray(proof.profiles) || proof.profiles.length > 4096 || !Array.isArray(proof.inventory) || proof.inventory.length > 65535) throw new Error('unverified common publication');
    let budget = MAX_BYTES - 1024 - 512;
    function charge(n) { budget -= n; if (budget < 0) throw new Error('common metadata too large'); }
    function name(value) { const n = text(value, MAX_BYTES); if (!n || n.trim() !== n) throw new Error('invalid publication name'); return n; }
    const byteLength = value => new TextEncoder().encode(value).byteLength;
    const locations = new Map(), worlds = new Map();
    for (const item of proof.locations) {
      if (!item || !Number.isInteger(item.channel) || item.channel < 1 || item.channel > 65535) throw new Error('invalid configured location');
      const world = name(item.world), key = JSON.stringify([world, item.channel]);
      if (locations.has(key)) throw new Error('duplicate configured location');
      charge(64 + 6 * byteLength(world)); locations.set(key, locations.size);
      if (!worlds.has(world)) worlds.set(world, []); worlds.get(world).push(item.channel);
    }
    const registry = new Map();
    for (const item of proof.profiles) {
      if (!item || !Array.isArray(item.worlds) || !item.worlds.length || item.worlds.length > 65535) throw new Error('invalid publication profile');
      const id = name(item.id), names = item.worlds.map(name), channels = item.channels === undefined ? [] : item.channels;
      if (registry.has(id) || new Set(names).size !== names.length || !Array.isArray(channels) || channels.length > 65535 || new Set(channels).size !== channels.length || channels.some(n => !Number.isInteger(n) || n < 1 || n > 65535)) throw new Error('duplicate or invalid publication profile');
      charge(128 + 6 * byteLength(id) + 24 * channels.length);
      for (const world of names) charge(16 + 6 * byteLength(world));
      registry.set(id, {id, worlds:names, channels:channels.slice()});
    }
    const inventory = new Map();
    for (const item of proof.inventory) {
      if (!item || !Number.isInteger(item.quest_id) || item.quest_id < 1 || item.quest_id > 65535 || inventory.has(item.quest_id) || !Array.isArray(item.profiles) || !item.profiles.length || item.profiles.length > 4096 || new Set(item.profiles).size !== item.profiles.length) throw new Error('invalid publication inventory');
      charge(64); const ids = item.profiles.map(name);
      for (const id of ids) { if (!registry.has(id)) throw new Error('unknown inventory profile'); charge(16 + 6 * byteLength(id)); }
      inventory.set(item.quest_id, ids);
    }
    const wordCount = Math.ceil(locations.size / 32);
    if (wordCount * registry.size * 4 > MAX_BYTES) throw new Error('common coverage working set too large');
    const masks = new Map();
    for (const profile of registry.values()) {
      if (profile.channels.length && profile.worlds.length > Math.floor(locations.size / profile.channels.length)) throw new Error('profile includes unavailable locations');
      const mask = new Uint32Array(wordCount); let count = 0;
      for (const world of profile.worlds) {
        if (!worlds.has(world)) throw new Error('profile references unknown or disabled world');
        for (const channel of (profile.channels.length ? profile.channels : worlds.get(world))) {
          const index = locations.get(JSON.stringify([world, channel]));
          if (index === undefined) throw new Error('profile references unknown or disabled channel');
          const bit = 1 << (index % 32), word = Math.floor(index / 32);
          if (!(mask[word] & bit)) { mask[word] |= bit; count++; }
        }
      }
      masks.set(profile.id, {mask, full:count === locations.size});
    }
    const scratch = new Uint32Array(wordCount);
    for (const ids of inventory.values()) {
      scratch.fill(0); let full = false;
      for (const id of ids) {
        const profile = masks.get(id);
        if (profile.full) { full = true; break; }
        for (let i = 0; i < wordCount; i++) scratch[i] |= profile.mask[i];
      }
      if (!full) for (let i = 0; i < wordCount; i++) {
        const last = i === wordCount - 1 && locations.size % 32;
        const wanted = last ? ((1 << last) - 1) >>> 0 : 0xffffffff;
        if (scratch[i] !== wanted) throw new Error('inventory does not cover all configured locations');
      }
    }
    return {registry, inventory, locationCount:locations.size, charge, byteLength};
  }

  function validateDocument(raw, expectedFrom, days) {
    if (!raw || raw.coverage !== 'managed_quests_only' || raw.timezone !== 'Asia/Seoul' || raw.original_entry_conditions_apply !== true) throw new Error('invalid schedule envelope');
    const common = validateCommonPublication(raw);
    const version = text(raw.version, 128);
    if (!/^[\x21-\x7e](?:[\x20-\x7e]*[\x21-\x7e])?$/.test(version)) throw new Error('invalid version');
    const from = timestamp(raw.from), until = timestamp(raw.until);
    if (from !== midnight(expectedFrom) || until !== from + days * DAY) throw new Error('schedule range mismatch');
    if (!Array.isArray(raw.occurrences) || raw.occurrences.length > MAX_ROWS) throw new Error('invalid occurrences');
    const rows = raw.occurrences.map(function (row) {
      if (!row || !Number.isInteger(row.quest_id) || row.quest_id < 1 || row.quest_id > 65535) throw new Error('invalid quest ID');
      const start = timestamp(row.starts_at), end = timestamp(row.ends_at);
      if (start >= end || start >= until || end <= from) throw new Error('invalid publication window');
      if (!Array.isArray(row.profiles) || !row.profiles.length || row.profiles.length > 4096) throw new Error('invalid profiles');
      const seenProfiles = new Set();
      const profiles = row.profiles.map(function (p) {
        if (!p || !Array.isArray(p.worlds) || !p.worlds.length || p.worlds.length > 65535) throw new Error('invalid worlds');
        const worlds = p.worlds.map(w => text(w, MAX_BYTES));
        const channels = p.channels === undefined ? [] : p.channels;
        if (!Array.isArray(channels) || channels.length > 65535 || channels.some(c => !Number.isInteger(c) || c < 1 || c > 65535)) throw new Error('invalid channels');
        const id = text(p.id, MAX_BYTES), registered = common.registry.get(id);
        if (!registered || seenProfiles.has(id) || JSON.stringify(worlds) !== JSON.stringify(registered.worlds) || JSON.stringify(channels) !== JSON.stringify(registered.channels)) throw new Error('occurrence profile differs from common registry');
        seenProfiles.add(id);
        return { id, worlds, channels: channels.slice() };
      });
      const assigned = common.inventory.get(row.quest_id);
      if (!assigned || assigned.length !== seenProfiles.size || assigned.some(id => !seenProfiles.has(id))) throw new Error('occurrence differs from enabled publication inventory');
      const requirements = row.requirements === undefined ? [] : row.requirements;
      if (!Array.isArray(requirements) || requirements.length > 64) throw new Error('invalid requirements');
      if (row.permanent !== undefined && typeof row.permanent !== 'boolean') throw new Error('invalid permanent flag');
      if (row.permanent === true && (start !== from || end !== until)) throw new Error('permanent range mismatch');
      const title = row.title === undefined ? '' : text(row.title, MAX_BYTES), scheduleID = text(row.schedule_id, MAX_BYTES);
      const preservedRequirements = requirements.map(r => text(r, 4096));
      let cost = 512 + 6 * (common.byteLength(title) + common.byteLength(scheduleID));
      for (const value of preservedRequirements) cost += 16 + 6 * common.byteLength(value);
      for (const profile of profiles) { cost += 128 + 6 * common.byteLength(profile.id) + 24 * profile.channels.length; for (const world of profile.worlds) cost += 16 + 6 * common.byteLength(world); }
      common.charge(cost);
      return { questID: row.quest_id, title, permanent: row.permanent === true,
        scheduleID, start, end, profiles, commonPublicationVerified:true,
        requirements: preservedRequirements };
    });
    return { version, coverage: raw.coverage, publicationScope:raw.publication_scope, locationCount:common.locationCount, inventoryCount:common.inventory.size, from, until, rows };
  }
  function openAt(row, now) { return row.start <= now && now < row.end; }
  function matches(row, query) {
    const term = String(query || '').normalize('NFKC').toLocaleLowerCase('ko');
    return !term || (row.questID + ' ' + row.title).normalize('NFKC').toLocaleLowerCase('ko').includes(term);
  }
  function groupDay(rows, key, query) {
    const from = midnight(key), until = from + DAY, grouped = new Map();
    for (const row of rows) {
      if (row.start < until && row.end > from && matches(row, query)) {
        if (!grouped.has(row.questID)) grouped.set(row.questID, { questID: row.questID, title: row.title, windows: [] });
        grouped.get(row.questID).windows.push(row);
      }
    }
    return [...grouped.values()].sort((a, b) => a.questID - b.questID);
  }
  function locationLabels(row) {
    if (!row || row.commonPublicationVerified !== true) throw new Error('unverified common row');
    return ['모든 기존 월드·채널 공통'];
  }
  function formatTime(n) {
    const d = new Date(n + KST);
    return d.toISOString().slice(0, 16).replace('T', ' ');
  }
  function detailURL(id, links) {
    const href = links && links[String(id)];
    if (typeof href !== 'string') return null;
    try {
      const u = new URL(href, 'https://ferias.bulzipke.com/');
      if (u.origin !== 'https://ferias.bulzipke.com' || !/^\/quest\/[a-zA-Z0-9_-]+\.htm$/.test(u.pathname) || !/^#l[0-9a-fA-F]+$/.test(u.hash)) return null;
      return u.href;
    } catch (_) { return null; }
  }
  async function fetchDocument(endpoint, from, days, fetcher, signal) {
    const url = new URL(endpoint, typeof location === 'object' ? location.href : 'https://ferias.bulzipke.com/');
    if (!['http:', 'https:'].includes(url.protocol)) throw new Error('unsupported schedule URL');
    // Static exports ignore these harmless query parameters; the API uses them.
    url.searchParams.set('from', from); url.searchParams.set('days', String(days));
    const response = await fetcher(url.href, { signal, credentials: 'omit', cache: 'no-cache' });
    if (!response.ok) throw new Error('schedule HTTP ' + response.status);
    const length = Number(response.headers && response.headers.get('Content-Length'));
    if (length > MAX_BYTES) throw new Error('schedule too large');
    // Limit decoded UTF-8 bytes even when Content-Length is absent/compressed.
    let body;
    if (response.body && response.body.getReader) {
      const reader = response.body.getReader(); let count = 0; const parts = [];
      try {
        for (;;) {
          const part = await reader.read();
          if (part.done) break;
          count += part.value.byteLength;
          if (count > MAX_BYTES) throw new Error('schedule too large');
          parts.push(part.value);
        }
      } catch (err) { await reader.cancel(); throw err; }
      const joined = new Uint8Array(count); let at = 0;
      for (const part of parts) { joined.set(part, at); at += part.byteLength; }
      body = new TextDecoder('utf-8', { fatal: true }).decode(joined);
    } else {
      body = await response.text();
      if (new TextEncoder().encode(body).byteLength > MAX_BYTES) throw new Error('schedule too large');
    }
    return validateDocument(JSON.parse(body), from, days);
  }
  function mount(container, options) {
    options = options || {};
    const doc = container.ownerDocument, endpoint = options.endpoint || container.dataset.scheduleUrl || '/v2/quests/schedule';
    const days = 30, clock = options.now || (() => Date.now()), fetcher = options.fetch || fetch.bind(globalThis);
    let model = null, selection = dateKey(clock()), query = '', limit = 60, serial = 0, disposed = false, controller = null;
    const elements = {};
    function el(tag, className, value) {
      const n = doc.createElement(tag); if (className) n.className = className;
      if (value !== undefined) n.textContent = value;
      return n;
    }
    container.classList.add('eq-calendar');
    const heading = el('h2', '', '퀘스트 배포 일정');
    container.appendChild(heading);
    if (options.preview) container.appendChild(el('p', 'eq-preview', '미리보기 · 운영 미적용'));
    container.appendChild(el('p', 'eq-note', '한국 시간(KST) 기준 · 기존 랭크·장비·입장권·진행 조건이 적용됩니다.'));
    container.appendChild(el('p', 'eq-note', '일정표에 편성된 퀘스트만 집계합니다. 기존 접수처의 전체 목록은 게임에서 확인하세요.'));
    container.appendChild(el('p', 'eq-note', '종료 시각부터 새로 출발할 수 없습니다. 진행 중인 사냥은 마칠 수 있습니다.'));
    container.appendChild(el('p', 'eq-note', '날짜별 개수는 그날 열리는 퀘스트 수이며, 동시에 열리는 수와 다를 수 있습니다.'));
    const controls = el('div', 'eq-controls');
    const searchLabel = el('label', '', '퀘스트 검색 '), search = el('input');
    search.type = 'search'; search.placeholder = '퀘스트명 또는 ID'; searchLabel.appendChild(search); controls.appendChild(searchLabel);
    const refresh = el('button', '', '새로고침'); refresh.type = 'button'; controls.appendChild(refresh); container.appendChild(controls);
    elements.status = el('p', 'eq-status', '일정을 불러오는 중입니다.'); elements.status.setAttribute('role', 'status'); container.appendChild(elements.status);
    elements.scope = el('p', 'eq-scope'); container.appendChild(elements.scope);
    elements.range = el('p', 'eq-range'); container.appendChild(elements.range);
    elements.grid = el('div', 'eq-days'); elements.grid.setAttribute('role', 'group'); elements.grid.setAttribute('aria-label', '30일 배포 달력'); container.appendChild(elements.grid);
    elements.detailHeading = el('h3'); container.appendChild(elements.detailHeading);
    elements.list = el('div', 'eq-list'); container.appendChild(elements.list);
    elements.more = el('button', 'eq-more', '더 보기'); elements.more.type = 'button'; elements.more.hidden = true; container.appendChild(elements.more);
    function renderDetails() {
      elements.list.replaceChildren(); elements.more.hidden = true;
      if (!model) { elements.detailHeading.textContent = ''; return; }
      const groups = groupDay(model.rows, selection, query), now = clock();
      elements.detailHeading.textContent = selection + ' · 당일 공개 ' + groups.length + '개';
      if (!groups.length) { elements.list.appendChild(el('p', 'eq-empty', '이 날짜와 검색 조건에 해당하는 배포 일정이 없습니다.')); return; }
      for (const group of groups.slice(0, limit)) {
        const article = el('article', 'eq-quest'), title = el('h4');
        const href = detailURL(group.questID, options.links);
        const label = group.title || '퀘스트 ' + group.questID;
        if (href) { const a = el('a', '', label); a.href = href; a.target = '_blank'; a.rel = 'noopener'; title.appendChild(a); }
        else title.textContent = label;
        title.appendChild(el('small', '', ' #' + group.questID)); article.appendChild(title);
        const active = group.windows.some(w => openAt(w, now));
        const future = group.windows.some(w => w.start > now);
        article.appendChild(el('p', 'eq-state ' + (active ? 'eq-open' : ''), active ? '공개 중' : future ? '공개 예정' : '공개 종료'));
        for (const row of group.windows) {
          article.appendChild(el('p', 'eq-window', row.permanent ? '상시 제공' : formatTime(row.start) + ' ~ ' + formatTime(row.end) + ' KST'));
          article.appendChild(el('p', 'eq-location', locationLabels(row).join(' · ')));
          if (row.requirements.length) article.appendChild(el('p', 'eq-requirements', row.requirements.join(' / ')));
        }
        elements.list.appendChild(article);
      }
      elements.more.hidden = groups.length <= limit;
      elements.more.textContent = '더 보기 (' + Math.min(limit, groups.length) + '/' + groups.length + ')';
    }
    function render() {
      elements.grid.replaceChildren();
      if (!model) { elements.scope.textContent = ''; elements.range.textContent = ''; renderDetails(); return; }
      elements.scope.textContent = '모든 기존 월드·채널 공통';
      elements.range.textContent = dateKey(model.from) + ' ~ ' + dateKey(model.until - DAY) + ' · KST';
      container.dataset.scheduleVersion = model.version;
      const weekdays = ['일', '월', '화', '수', '목', '금', '토'];
      for (const name of weekdays) elements.grid.appendChild(el('span', 'eq-weekday', name));
      const leading = new Date(model.from + KST).getUTCDay();
      for (let i = 0; i < leading; i++) elements.grid.appendChild(el('span', 'eq-pad'));
      for (let at = model.from; at < model.until; at += DAY) {
        const key = dateKey(at), groups = groupDay(model.rows, key, query);
        const button = el('button', 'eq-day' + (key === dateKey(clock()) ? ' eq-today' : ''));
        button.type = 'button'; button.setAttribute('aria-pressed', String(key === selection));
        button.setAttribute('aria-label', key + ', ' + groups.length + '개 퀘스트');
        button.appendChild(el('span', '', key.slice(5).replace('-', '/')));
        button.appendChild(el('small', '', groups.length + '개'));
        button.addEventListener('click', () => { selection = key; limit = 60; render(); });
        elements.grid.appendChild(button);
      }
      renderDetails();
    }
    async function refreshData() {
      if (disposed) return;
      const current = ++serial, requestedFrom = dateKey(clock());
      if (controller) controller.abort(); controller = new AbortController();
      elements.status.textContent = '일정을 불러오는 중입니다.'; refresh.disabled = true;
      try {
        const next = await fetchDocument(endpoint, requestedFrom, days, fetcher, controller.signal);
        if (disposed || current !== serial) return;
        model = next;
        if (midnight(selection) < next.from || midnight(selection) >= next.until) selection = requestedFrom;
        elements.status.textContent = '최근 확인 ' + formatTime(clock()) + ' KST'; render();
      } catch (_) {
        if (disposed || current !== serial) return;
        model = null; render();
        elements.status.textContent = '일정을 불러오지 못했습니다. 새로고침으로 다시 확인해 주세요.';
      } finally { if (!disposed && current === serial) refresh.disabled = false; }
    }
    search.addEventListener('input', () => { query = search.value; limit = 60; render(); });
    refresh.addEventListener('click', refreshData);
    elements.more.addEventListener('click', () => { limit += 60; renderDetails(); });
    const timer = setInterval(() => { if (!doc.hidden) refreshData(); }, 60000);
    function visible() { if (!doc.hidden) refreshData(); }
    doc.addEventListener('visibilitychange', visible);
    const ready = refreshData();
    return { ready, refresh: refreshData, destroy() { disposed = true; serial++; clearInterval(timer); if (controller) controller.abort(); doc.removeEventListener('visibilitychange', visible); container.replaceChildren(); } };
  }
  return { dateKey, midnight, validateDocument, openAt, groupDay, matches, locationLabels, detailURL, fetchDocument, mount };
});
