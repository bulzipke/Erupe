const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const html = fs.readFileSync(path.join(__dirname, "dashboard.html"), "utf8");
function section(start, end) {
    const from = html.indexOf(start);
    const to = html.indexOf(end, from);
    assert.ok(from >= 0 && to > from, start);
    return html.slice(from, to);
}
const settingsCode = section("    function huntSettingsDescription(", "\n\tfunction ravienteDurationLabel");
const controlCode = section("    function buildMonsterTimeNameControl(", "    function renderMonsterTimes(");
const bindingCode = section("\tfunction positionRavienteParticipants(", "\tfunction buildRavienteRunTable(");
const functions = vm.runInNewContext(settingsCode + "\n({huntSettingsDescription,huntTimeLabel})");

test("missing or unsupported settings display HP not used", () => {
    for (const clientSettings of [null, undefined, {}, {version: 2}, {version: 1}, {version: 1, hpView: false, hpViewUsed: false}]) {
        assert.equal(functions.huntSettingsDescription({clientSettings}), "HP표시미사용");
    }
});
test("HP enabled at the end or earlier in the hunt displays used; other options do not", () => {
    for (const fields of [{hpView: true}, {hpView: false, hpViewUsed: true}]) {
        assert.equal(functions.huntSettingsDescription({clientSettings: {version: 1, ...fields}}), "HP표시사용");
    }
    assert.equal(functions.huntSettingsDescription({clientSettings: {version: 1, elapsedMode: 2, elapsedUsed: true, fov: 60, fovUsed: true}}), "HP표시미사용");
});
test("hunt digits retain native centisecond truncation", () => {
    for (const [frames, want] of [[0,"00:00.00"],[1,"00:00.03"],[2,"00:00.06"],[29,"00:00.96"],[30,"00:01.00"],[1799,"00:59.96"],[1800,"01:00.00"],[8051,"04:28.36"],[108000,"60:00.00"]]) {
        assert.equal(functions.huntTimeLabel(frames), want);
    }
});

function fixture(nativePopover) {
    let nextTimer = 0;
    const timers = new Map();
    class Element {
        constructor(tag) {
            this.tag = tag; this.children = []; this.attrs = {}; this.listeners = {};
            this.dataset = {}; this.style = {}; this.textContent = ""; this.title = "";
            this.hidden = false; this.open = false; this.isConnected = true;
            this.rect = {left: 20, top: 30, bottom: 50, width: 140, height: 40};
            if (nativePopover) {
                this.showPopover = () => { this.open = true; this.fire("toggle", {newState: "open"}); };
                this.hidePopover = () => { this.open = false; this.fire("toggle", {newState: "closed"}); };
            }
        }
        appendChild(child) { this.children.push(child); return child; }
        setAttribute(key, value) { this.attrs[key] = value; }
        getAttribute(key) { return this.attrs[key] ?? null; }
        removeAttribute(key) { delete this.attrs[key]; }
        addEventListener(type, listener) { (this.listeners[type] ||= []).push(listener); }
        fire(type, event = {}) { for (const listener of this.listeners[type] || []) listener(event); }
        matches(selector) { assert.equal(selector, ":popover-open"); return this.open; }
        getBoundingClientRect() { return this.rect; }
        get cells() { return this.children.filter(child => child.tag === "td"); }
    }
    const context = vm.createContext({
        document: {createElement: tag => new Element(tag)},
        text(tag, cls, value) { const element = new Element(tag); element.className = cls; element.textContent = value; return element; },
        buildMonsterTimeWeaponCell: () => new Element("td"),
        numberFormatter: new Intl.NumberFormat("ko-KR"), participantNameCollator: new Intl.Collator("ko-KR"),
        window: {innerWidth: 390, innerHeight: 844}, requestAnimationFrame: callback => callback(),
        setTimeout(callback) { const id = ++nextTimer; timers.set(id, callback); return id; },
        clearTimeout(id) { timers.delete(id); },
    });
    vm.runInContext(settingsCode + controlCode + bindingCode, context);
    return {
        context,
        flush() { const pending = Array.from(timers.values()); timers.clear(); pending.forEach(callback => callback()); },
        isOpen(popover) { return nativePopover ? popover.open : !popover.hidden; },
    };
}

for (const nativePopover of [true, false]) {
    test(`name popover supports hover, focus and tap toggle (${nativePopover ? "native" : "fallback"})`, () => {
        const h = fixture(nativePopover);
        const cell = h.context.buildMonsterTimeNameControl({name: "카칸", clientSettings: {version: 1, hpViewUsed: true}}, "hp-test");
        const [trigger, popover] = cell.children;
        assert.equal(trigger.textContent, "카칸");
        assert.equal(trigger.type, "button");
        assert.equal(popover.textContent, "HP표시사용");
        assert.equal(popover.getAttribute("role"), "tooltip");
        assert.equal(trigger.getAttribute("aria-describedby"), popover.id);
        assert.equal(h.isOpen(popover), false);
        trigger.fire("mouseenter");
        assert.equal(h.isOpen(popover), true);
        trigger.fire("mouseleave"); popover.fire("mouseenter"); h.flush();
        assert.equal(h.isOpen(popover), true);
        popover.fire("mouseleave"); h.flush();
        assert.equal(h.isOpen(popover), false);
        trigger.fire("focus"); assert.equal(h.isOpen(popover), true);
        trigger.fire("blur"); h.flush(); assert.equal(h.isOpen(popover), false);
        trigger.fire("click"); trigger.fire("mouseleave"); h.flush();
        assert.equal(h.isOpen(popover), true);
        assert.equal(trigger.getAttribute("aria-expanded"), "true");
        trigger.fire("click");
        assert.equal(h.isOpen(popover), false);
        assert.equal(trigger.getAttribute("aria-expanded"), "false");
        if (nativePopover) {
            trigger.fire("click"); popover.hidePopover(); // Native outside tap / Escape.
            assert.equal(trigger.getAttribute("aria-expanded"), "false");
            trigger.fire("click"); assert.equal(h.isOpen(popover), true);
        }
    });
}
test("popup stays in the narrow viewport and opens above a low name", () => {
    const h = fixture(true);
    const [trigger, popover] = h.context.buildMonsterTimeNameControl({name: "Lab0r"}, "hp-edge").children;
    trigger.rect = {left: 380, top: 830, bottom: 850};
    popover.rect = {width: 150, height: 40};
    trigger.fire("click");
    assert.equal(popover.style.left, "228px");
    assert.equal(popover.style.top, "782px");
    assert.equal(popover.textContent, "HP표시미사용");
});
test("the real hunt row attaches HP only to its name and keeps text safe and IDs unique", () => {
    const h = fixture(true);
    const entry = {name: "<img src=x>", frames: 8051, questName: "테스트 퀘스트"};
    const first = h.context.buildMonsterTimeCard({key: "6:g:normal", name: "몬스터"}, [entry, entry]);
    const second = h.context.buildMonsterTimeCard({key: "6:g:hardcore", name: "몬스터"}, [entry]);
    const rows = card => card.children[1].children[0].children[0].children;
    const all = [...rows(first), ...rows(second)];
    const ids = all.map(row => row.cells[1].children[1].id);
    assert.equal(new Set(ids).size, 3);
    for (const row of all) {
        assert.equal(row.title, "");
        assert.equal(row.cells[1].children[0].textContent, entry.name);
        assert.equal(row.cells[1].children[1].textContent, "HP표시미사용");
        assert.equal(row.cells[3].textContent, "04:28.36");
        assert.equal(row.cells[3].title, "");
        assert.equal(row.cells[4].title, "테스트 퀘스트");
    }
});
test("the shared binding retains Raviente participant names and tap behavior", () => {
    const h = fixture(true);
    const [trigger, popover] = h.context.buildRavienteParticipantControl({id: 7, participantCount: 2, participants: ["나", "가"]}).children;
    assert.equal(trigger.textContent, "2명");
    assert.equal(popover.getAttribute("role"), "dialog");
    assert.deepEqual(popover.children[1].children.map(child => child.textContent), ["가", "나"]);
    trigger.fire("click"); assert.equal(h.isOpen(popover), true);
    trigger.fire("click"); assert.equal(h.isOpen(popover), false);
});
