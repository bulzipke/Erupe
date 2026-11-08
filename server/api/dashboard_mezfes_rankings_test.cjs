const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const html = fs.readFileSync(path.join(__dirname, "dashboard.html"), "utf8");
const rankingCode = html.slice(html.indexOf("    function renderRanking("), html.indexOf("    // Quest rows"));
const mezFesCode = html.slice(html.indexOf("    var mezFesStages"), html.indexOf("    function renderLegendRanking("));
class Element {
  constructor(tag) { this.tag = tag; this.children = []; this.textContent = ""; }
  appendChild(child) { this.children.push(child); return child; }
  replaceChildren(...children) { this.children = children; }
}
function fixture() {
  const nodes = new Map();
  const byId = id => { if (!nodes.has(id)) nodes.set(id, new Element("ol")); return nodes.get(id); };
  const context = vm.createContext({ byId, numberFormatter: new Intl.NumberFormat("ko-KR"),
    document: { createElement: tag => new Element(tag), createDocumentFragment: () => new Element("fragment") },
    text(tag, cls, content) { const e = new Element(tag); e.className = cls; e.textContent = content; return e; } });
  vm.runInContext(rankingCode + mezFesCode, context);
  return { byId, render: entries => { context.entries = entries; vm.runInContext("renderMezFesRankings(entries)", context); } };
}
test("entries land on their own minigame card with only the score", () => {
  const h = fixture();
  h.render([
    { stageId: 465, name: "볼파", bestScore: 64600, bestRank: 1, bestPoints: 1200, plays: 3 },
    { stageId: 467, name: "<b>a</b>", bestScore: 7705, bestRank: 3, bestPoints: 638, plays: 1 },
    { stageId: 467, name: "b", bestScore: null, bestPoints: 500, plays: 12 },
  ]);
  const solo = h.byId("mezfes-ranking-467").children[0].children;
  assert.equal(solo.length, 2);
  assert.equal(solo[0].children[1].textContent, "<b>a</b>");
  assert.equal(solo[0].children[2].textContent, "7,705점");
  assert.equal(solo[1].children[2].textContent, "500pt");
  assert.equal(h.byId("mezfes-ranking-465").children[0].children[0].children[2].textContent, "64,600점");
});
test("minigames without plays show the empty state", () => {
  const h = fixture();
  h.render([]);
  for (const stage of [463, 464, 465, 466, 467, 468, 469]) {
    assert.equal(h.byId("mezfes-ranking-" + stage).children[0].className, "ranking-empty");
  }
});
