const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const html = fs.readFileSync(path.join(__dirname,"dashboard.html"),"utf8");
const start = html.indexOf("    function huntSettingsDescription(");
const end = html.indexOf("\n\tfunction ravienteDurationLabel",start);
assert.ok(start>=0 && end>start);
const functions = vm.runInNewContext(html.slice(start,end)+"\n({huntSettingsDescription,huntTimeLabel})");
test("historical settings are unknown",()=>{
    for (const clientSettings of [null,undefined,{}, {version:2}]) {
        const text=functions.huntSettingsDescription({clientSettings});
        assert.equal(text,"몬스터 HP 표시: 미수집\n시간 표시: 미수집");
    }
});
test("hover includes frozen settings and earlier usage",()=>{
    const text=functions.huntSettingsDescription({clientSettings:{version:1,hpView:false,hpViewUsed:true,elapsedMode:2,elapsedUsed:true,fov:60,fovUsed:true}});
    assert.equal(text,"몬스터 HP 표시: OFF (퀘스트 중 ON 사용)\n시간 표시: 클리어 시 표시");
    assert.match(functions.huntSettingsDescription({clientSettings:{version:1,hpView:false,elapsedMode:0,elapsedUsed:true,fov:45,fovUsed:false}}),/시간 표시: OFF \(퀘스트 중 사용\)/);
});
test("hunt digits truncate centiseconds like the native Brook Tower HUD",()=>{
    for (const [frames,want] of [[0,"00:00.00"],[1,"00:00.03"],[2,"00:00.06"],[29,"00:00.96"],[30,"00:01.00"],[1799,"00:59.96"],[1800,"01:00.00"],[8051,"04:28.36"],[108000,"60:00.00"]]) assert.equal(functions.huntTimeLabel(frames),want);
});
test("every hunt cell gets the metadata tooltip",()=>{
    assert.match(html,/row\.title = settingsDescription/);
    assert.match(html,/cell\.title = \(cell\.title \? cell\.title \+ "\\n" : ""\) \+ settingsDescription/);
});
