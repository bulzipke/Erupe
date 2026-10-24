(function () {
  'use strict';
  const scriptURL = document.currentScript.src;
  const calendar = document.getElementById('erupe-quest-calendar');
  if (calendar) {
    const controller = new AbortController();
    const timeout = setTimeout(() => controller.abort(), 5000);
    fetch(new URL('quest-links.json', scriptURL), {credentials:'omit',signal:controller.signal})
      .then(r => { if (!r.ok) throw new Error('links unavailable'); return r.json(); })
      .catch(() => ({}))
      .then(links => ErupeQuestCalendar.mount(calendar, {links}))
      .finally(() => clearTimeout(timeout));
  }
  const details = document.getElementById('erupe-quest-details');
  if (details) {
    function placeDetails() {
      const id = ErupeQuestDetails.parseQuestID(location.hash);
      if (id === null) return;
      const names = [location.hash.slice(1), 'l' + id.toString(16).toUpperCase(), 'l' + id.toString(16)];
      let anchor = null;
      for (const name of names) { anchor = document.getElementById(name) || document.getElementsByName(name)[0]; if (anchor) break; }
      const table = anchor && anchor.closest('table');
      if (table && !details.contains(table)) {
        table.parentNode.insertBefore(details, table);
        details.scrollIntoView({block:'start'});
      }
    }
    const widget = ErupeQuestDetails.mount(details);
    widget.ready.then(placeDetails);
    window.addEventListener('hashchange', placeDetails);
  }
})();
