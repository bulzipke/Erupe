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
const timeCode = section("    function huntTimeLabel(", "\n\tfunction ravienteDurationLabel");
const controlCode = section("    function buildMonsterTimeCard(", "    function renderMonsterTimes(");
const bindingCode = section("\tfunction positionRavienteParticipants(", "\tfunction buildRavienteRunTable(");
const functions = vm.runInNewContext(timeCode + "\n({huntTimeLabel})");
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
    vm.runInContext(timeCode + controlCode + bindingCode, context);
    return {
        context,
        flush() { const pending = Array.from(timers.values()); timers.clear(); pending.forEach(callback => callback()); },
        isOpen(popover) { return nativePopover ? popover.open : !popover.hidden; },
    };
}

for (const nativePopover of [true, false]) {
    test(`Raviente participants support hover, focus and tap toggle (${nativePopover ? "native" : "fallback"})`, () => {
        const h = fixture(nativePopover);
        const cell = h.context.buildRavienteParticipantControl({id: 7, participantCount: 2, participants: ["나", "가"]});
        const [trigger, popover] = cell.children;
        assert.equal(trigger.textContent, "2명");
        assert.equal(trigger.type, "button");
        assert.equal(popover.getAttribute("role"), "dialog");
        assert.equal(trigger.getAttribute("aria-controls"), popover.id);
        assert.deepEqual(popover.children[1].children.map(child => child.textContent), ["가", "나"]);
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
test("Raviente participant popup stays in the narrow viewport", () => {
    const h = fixture(true);
    const [trigger, popover] = h.context.buildRavienteParticipantControl({id: 8, participants: ["Lab0r"]}).children;
    trigger.rect = {left: 380, top: 830, bottom: 850};
    popover.rect = {width: 150, height: 40};
    trigger.fire("click");
    assert.equal(popover.style.left, "228px");
    assert.equal(popover.style.top, "782px");
    assert.equal(popover.children[1].children[0].textContent, "Lab0r");
});
test("hunt names remain plain text for all client settings and keep time and quest labels", () => {
    const h = fixture(true);
    const entry = {name: "<img src=x>", frames: 8051, questName: "테스트 퀘스트"};
    const settings = [undefined, null, {}, {version: 2}, {version: 1, hpView: true}, {version: 1, hpViewUsed: true},
        {version: 1, hpView: false, hpViewUsed: false, elapsedMode: 2, elapsedUsed: true, fov: 60, fovUsed: true, supportUsed: true}];
    const first = h.context.buildMonsterTimeCard({key: "6:g:normal", name: "몬스터"}, settings.map(clientSettings => ({...entry, clientSettings})));
    const second = h.context.buildMonsterTimeCard({key: "6:g:hardcore", name: "몬스터"}, [entry]);
    const rows = card => card.children[1].children[0].children[0].children;
    const all = [...rows(first), ...rows(second)];
    assert.equal(all.length, settings.length + 1);
    for (const row of all) {
        assert.equal(row.title, "");
        const name = row.cells[1];
        assert.equal(name.textContent, entry.name);
        assert.equal(name.className, "monster-time-name");
        assert.equal(name.children.length, 0);
        assert.equal(name.title, "");
        assert.deepEqual(name.attrs, {});
        assert.deepEqual(name.listeners, {});
        assert.equal(row.cells[3].textContent, "04:28.36");
        assert.equal(row.cells[3].title, "");
        assert.equal(row.cells[4].title, "테스트 퀘스트");
    }
});
