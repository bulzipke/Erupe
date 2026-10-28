const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const { test } = require("node:test");
const html = fs.readFileSync(path.join(__dirname,"dashboard.html"),"utf8");
const rankingCode=html.slice(html.indexOf("    function renderRanking("),html.indexOf("    // Quest rows"));
const renderCode=html.slice(html.indexOf("    function render(data)"),html.indexOf("    function updateStats()"));
class Element {
 constructor(tag){this.tag=tag;this.children=[];this.textContent="";}
 appendChild(child){this.children.push(child);return child;}
 replaceChildren(...children){this.children=children;}
}
function fixture(){
 const nodes=new Map();const byId=id=>{if(!nodes.has(id))nodes.set(id,new Element("ol"));return nodes.get(id);};
 const noop=()=>{};
 const context=vm.createContext({byId,numberFormatter:new Intl.NumberFormat("ko-KR"),
  document:{createElement:tag=>new Element(tag),createDocumentFragment:()=>new Element("fragment")},
  text(tag,cls,content){const e=new Element(tag);e.className=cls;e.textContent=content;return e;},
  renderOnline:noop,renderQuestRanking:noop,renderWeaponRanking:noop,renderLegendRanking:noop,renderMonsterTimes:noop,renderRavienteRuns:noop,playtimeLabel:String});
 vm.runInContext(rankingCode+renderCode,context);return {byId,render:data=>{context.data=data;vm.runInContext("render(data)",context);}};
}
test("both cards render real aggregate counts as text",()=>{
 const h=fixture();h.render({rankings:{popularGachas:[{name:"<img src=x>",count:12345}],popularKits:[{name:"키트 A",count:7}]}});
 const g=h.byId("gacha-popularity-ranking").children[0].children[0];
 assert.equal(g.children[1].textContent,"<img src=x>");assert.equal(g.children[2].textContent,"12,345회");
 const k=h.byId("kit-popularity-ranking").children[0].children[0];assert.equal(k.children[1].textContent,"키트 A");assert.equal(k.children[2].textContent,"7회");
});
test("absent and empty ranking data show the existing empty state",()=>{
 const h=fixture();for(const data of [{},{rankings:{popularGachas:[],popularKits:[]}}]){
  h.render(data);for(const id of ["gacha-popularity-ranking","kit-popularity-ranking"])assert.equal(h.byId(id).children[0].textContent,"표시할 기록이 없습니다.");
 }
});
test("dashboard inline script parses",()=>{
 const scripts=[...html.matchAll(/<script[^>]*>([\s\S]*?)<\/script>/g)];assert.ok(scripts.length);for(const m of scripts)new vm.Script(m[1]);
});
