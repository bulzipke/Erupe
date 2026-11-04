const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const html = fs.readFileSync(path.join(__dirname, "dashboard.html"), "utf8");
const code = html.slice(html.indexOf("    function divaBeadDescription("), html.indexOf("    function buildOnlineChannelCell("));

function fixture() {
    let now = Date.parse("2026-11-04T12:00:00Z");
    class Clock extends Date { static now() { return now; } }
    const ctx = vm.createContext({Date: Clock, text(tag, cls, value) {
        return {className: cls, textContent: value, attrs: {}, events: {},
            setAttribute(k, v) { this.attrs[k] = v; }, removeAttribute(k) { delete this.attrs[k]; },
            addEventListener(k, v) { this.events[k] = v; }};
    }});
    vm.runInContext(code, ctx);
    return {ctx, advance() { now += 3600000; }, now};
}

test("online hunter hover shows all activated beads and removes expired help", () => {
    const h = fixture();
    const player = {name: "헌터", divaBeads: {round: 5, beads: ["결집", "봉뢰", "가호", "단력"], levels: [1, 2, 1, 1], expiresAt: "2026-11-04T12:30:00Z"}};
    const cell = h.ctx.buildOnlineHunterCell(player);
    assert.equal(cell.textContent, "헌터");
    assert.equal(cell.title, "사용 기주: 결집1, 봉뢰2, 가호1, 단력1");
    assert.match(cell.attrs["aria-label"], /헌터 · 사용 기주:/);
    assert.equal(cell.attrs.tabindex, "0");
    h.advance();
    cell.events.mouseenter();
    assert.equal(cell.title, "");
    assert.equal(cell.className, "hunter-name");
    assert.equal(cell.attrs["aria-label"], undefined);
});

test("unknown historical levels remain names only; a known zero is retained", () => {
    const {ctx} = fixture();
    const info = {round: 5, beads: ["결집", "봉뢰"]};
    for (const levels of [undefined, [], [1], [1, -1], [1, 4], [1, "2"], "AQI="]) {
        assert.equal(ctx.divaBeadDescription({...info, levels}), "사용 기주: 결집, 봉뢰");
    }
    assert.equal(ctx.divaBeadDescription({...info, levels: [0, 2]}), "사용 기주: 결집0, 봉뢰2");
});

test("inactive or invalid battle songs have no hover or focus decoration", () => {
    const h = fixture();
    for (const divaBeads of [undefined, null, {}, {round: 5, beads: []},
        {round: 5, beads: ["단력"], expiresAt: "2026-11-04T12:00:00Z"},
        {round: 5, beads: ["단력"], expiresAt: "invalid"}]) {
        const cell = h.ctx.buildOnlineHunterCell({name: "헌터", divaBeads});
        assert.equal(cell.title, "");
        assert.equal(cell.className, "hunter-name");
        assert.equal(cell.attrs.tabindex, undefined);
    }
});
