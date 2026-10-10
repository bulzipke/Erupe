const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { webcrypto } = require("node:crypto");
const { test } = require("node:test");

const html = fs.readFileSync(path.join(__dirname, "dashboard.html"), "utf8");
const deliveryCode = html.slice(html.indexOf("    var deliveryCharacters = []"), html.indexOf("    function renderRanking("));
assert.ok(deliveryCode.includes("function initDeliveryForm()"));

// Exercise the actual inline handlers with a small DOM double, without a browser
// or a real operator cookie. All network requests are mocked.
class Element {
    constructor(tag = "div") {
        this.tag = tag;
        this.children = [];
        this.listeners = {};
        this.attributes = {};
        this.value = "";
        this.hidden = false;
        this.disabled = false;
        this.classList = { toggle() {} };
    }
    replaceChildren(...children) {
        this.children = children;
        if (this.tag === "select") this.value = children[0]?.value || "";
    }
    appendChild(child) { this.children.push(child); }
    setAttribute(key, value) { this.attributes[key] = value; }
    removeAttribute(key) { delete this.attributes[key]; }
    addEventListener(type, handler) { this.listeners[type] = handler; }
    scrollIntoView() {}
    emit(type, values = {}) {
        const event = { target: this, prevented: false, preventDefault() { this.prevented = true; }, ...values };
        this.listeners[type]?.(event);
        return event;
    }
}

function harness(sharedStorage = new Map()) {
    const elements = new Map();
    const timers = new Map();
    const requests = [];
    let nextTimer = 0;
    const byId = (id) => {
        if (!elements.has(id)) elements.set(id, new Element(id === "delivery-character" ? "select" : "div"));
        return elements.get(id);
    };
    const context = vm.createContext({
        operatorMode: true, byId, AbortController, Uint8Array, Promise,
        numberFormatter: new Intl.NumberFormat("ko-KR"),
        text(tag, className, content) { const el = new Element(tag); el.className = className; el.textContent = content; return el; },
        Option: class extends Element { constructor(content, value) { super("option"); this.textContent = content; this.value = value; } },
        document: { addEventListener() {} },
        window: { crypto: webcrypto, confirm() { return true; } },
        sessionStorage: {
            getItem(key) { return sharedStorage.get(key) ?? null; },
            setItem(key, value) { sharedStorage.set(key, value); },
            removeItem(key) { sharedStorage.delete(key); },
        },
        setTimeout(callback) { const id = ++nextTimer; timers.set(id, callback); return id; },
        clearTimeout(id) { timers.delete(id); },
        fetch(url, options) {
            return new Promise((resolve, reject) => requests.push({ url, options, resolve, reject }));
        },
    });
    vm.runInContext(deliveryCode + "\ninitDeliveryForm();", context);
    return {
        context, byId, requests, storage: sharedStorage,
        run(code) { return vm.runInContext(code, context); },
        tick() { const callbacks = [...timers.values()]; timers.clear(); callbacks.forEach((callback) => callback()); },
        respond(request, data, status = 200) { request.resolve({ ok: status < 400, status, json: () => Promise.resolve(data) }); },
    };
}

async function flush() {
    for (let i = 0; i < 3; i++) await new Promise((resolve) => setImmediate(resolve));
}

const item = { id: 7, hex: "0007", name: "회복약" };
const character = { id: 10, code: "B11111", name: "테스트" };
function prepare(h) {
    h.context.deliveryCharacters = [character];
    h.byId("delivery-character").value = "10";
    h.byId("delivery-quantity").value = "199";
    h.context.testItem = item;
    h.run("chooseDeliveryItem(testItem)");
}

test("delivery panel starts hidden and uses only the existing operator state", () => {
    assert.match(html, /id="delivery-panel"[^>]*hidden/);
    assert.match(html, /byId\("delivery-panel"\)\.hidden = !enabled/);
    const h = harness();
    h.context.operatorMode = false;
    h.byId("delivery-item-query").value = "회복";
    h.byId("delivery-character-query").value = "테스트";
    h.run("searchDeliveryItems(false); searchDeliveryCharacters(); submitDelivery({preventDefault(){}})");
    assert.equal(h.requests.length, 0);
});

test("Korean IME composition waits until completion; Enter selects rather than submitting", async () => {
    const h = harness();
    const query = h.byId("delivery-item-query");
    query.value = "회복";
    query.emit("input", { isComposing: true });
    h.tick();
    assert.equal(h.requests.length, 0);
    query.emit("compositionend");
    h.tick();
    assert.equal(h.requests.length, 1);
    assert.ok(h.requests[0].url.includes(encodeURIComponent("회복")));
    h.respond(h.requests[0], { items: [item], total: 15713, updatedAt: new Date().toISOString() });
    await flush();
    assert.equal(h.byId("delivery-item-suggestions").hidden, false);
    assert.equal(query.emit("keydown", { key: "Enter", isComposing: true }).prevented, false);
    assert.equal(h.run("deliverySelectedItem"), null);
    assert.equal(query.emit("keydown", { key: "ArrowDown" }).prevented, true);
    assert.equal(query.emit("keydown", { key: "Enter" }).prevented, true);
    assert.equal(query.value, "회복약");
    assert.equal(h.run("deliverySelectedItem.id"), 7);
    assert.equal(h.byId("delivery-item-suggestions").hidden, true);
    assert.equal(h.requests.length, 1);
});

test("old autocomplete responses cannot replace newer Korean input", async () => {
    const h = harness();
    h.byId("delivery-item-query").value = "회";
    h.run("searchDeliveryItems(false)");
    h.byId("delivery-item-query").value = "꽃";
    h.run("searchDeliveryItems(false)");
    assert.equal(h.requests[0].options.signal.aborted, true);
    h.respond(h.requests[1], { items: [{ id: 42, hex: "002A", name: "꽃" }], total: 1, updatedAt: new Date().toISOString() });
    await flush();
    h.respond(h.requests[0], { items: [item], total: 1, updatedAt: new Date().toISOString() });
    await flush();
    assert.equal(h.run("deliveryItems[0].name"), "꽃");
    h.run("chooseDeliveryItem(deliveryItems[0])");
    h.byId("delivery-item-query").emit("input");
    assert.equal(h.run("deliverySelectedItem"), null);
});

test("character candidates show name and hunter code; manual refresh bypasses name cache", async () => {
    const h = harness();
    h.byId("delivery-character-query").value = "테스트";
    h.run("searchDeliveryCharacters()");
    h.respond(h.requests[0], { characters: [character] });
    await flush();
    assert.equal(h.byId("delivery-character").children[1].textContent, "테스트(B11111)");
    assert.equal(h.byId("delivery-character").disabled, false);
    prepare(h);
    h.byId("delivery-refresh").emit("click");
    assert.equal(h.run("deliverySelectedItem"), null);
    assert.match(h.requests[1].url, /&refresh=1$/);
});

test("unconfirmed delivery survives reload and retries exactly the same request", async () => {
    const h = harness();
    prepare(h);
    h.byId("delivery-form").emit("submit");
    const payload = h.requests[0].options.body;
    assert.equal(JSON.parse(payload).quantity, 199);
    assert.equal(h.byId("delivery-quantity").disabled, true);
    h.requests[0].reject(new Error("connection lost after commit"));
    await flush();
    assert.equal(h.byId("delivery-submit").textContent, "같은 요청 재시도");
    assert.equal(h.storage.size, 1);
    const reloaded = harness(h.storage);
    reloaded.run("restoreDeliveryRequest()");
    assert.equal(reloaded.byId("delivery-character-query").value, "테스트");
    reloaded.byId("delivery-form").emit("submit");
    assert.equal(reloaded.requests[0].options.body, payload);
    reloaded.respond(reloaded.requests[0], { ...JSON.parse(payload), alreadyDelivered: true, mailCount: 3 });
    await flush();
    assert.equal(reloaded.storage.size, 0);
    assert.equal(reloaded.byId("delivery-quantity").disabled, false);
    assert.match(reloaded.byId("delivery-feedback").textContent, /중복 지급하지 않았습니다/);
});

test("delivery validation failures clear pending request, while loss of operator permission preserves it", async () => {
    for (const status of [409, 403]) {
        const h = harness();
        prepare(h);
        h.byId("delivery-form").emit("submit");
        h.respond(h.requests[0], { error: "지급 거절" }, status);
        await flush();
        assert.equal(h.storage.size, status === 403 ? 1 : 0);
        assert.equal(h.byId("delivery-item-query").disabled, status === 403);
    }
});
