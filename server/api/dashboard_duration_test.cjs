const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const { test } = require("node:test");
const vm = require("node:vm");

const html = fs.readFileSync(path.join(__dirname, "dashboard.html"), "utf8");
const names = ["huntTimeLabel", "ravienteDurationLabel", "ravienteDurationDescription"];
const functions = names.map((name) => {
    const match = html.match(new RegExp(`function ${name}\\([^)]*\\) \\{[\\s\\S]*?\\n\\s*\\}`));
    assert.ok(match, `${name} must exist in the dashboard`);
    return match[0];
});
const format = vm.runInNewContext(functions.join("\n") + `\n({${names.join(",")}})`);

test("dashboard inline JavaScript is syntactically valid", () => {
    for (const match of html.matchAll(/<script(?:\s[^>]*)?>([\s\S]*?)<\/script>/g)) {
        new vm.Script(match[1]);
    }
});

for (const [durationMs, expected] of [
    [0, "00:00.00"],
    [null, "00:00.00"],
    [undefined, "00:00.00"],
    [-1, "00:00.00"],
    [1234, "00:01.23"],
    [1235, "00:01.24"],
    [59994, "00:59.99"],
    [59995, "01:00.00"],
    [3599995, "60:00.00"],
    [3600000, "60:00.00"],
    [4326000, "72:06.00"],
    [4326123, "72:06.12"],
    [6000123, "100:00.12"],
    [90000000, "1500:00.00"],
    ["4326000", "72:06.00"],
]) {
    test(`Raviente ${String(durationMs)} ms displays as ${expected}`, () => {
        assert.equal(format.ravienteDurationLabel(durationMs), expected);
    });
}

test("Raviente and normal hunts use the same minutes/seconds format", () => {
    for (const frames of [0, 1, 30, 2170, 129780, 2700000]) {
        assert.equal(format.ravienteDurationLabel(frames * 1000 / 30), format.huntTimeLabel(frames));
    }
});

test("Raviente accessible descriptions match the displayed total minutes", () => {
    assert.equal(format.ravienteDurationDescription(4326000), "72분 6초");
    assert.equal(format.ravienteDurationDescription(4326123), "72분 6.12초");
    assert.equal(format.ravienteDurationDescription(59995), "1분 0초");
});
