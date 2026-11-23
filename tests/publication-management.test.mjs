import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";

const read = name => readFileSync(new URL(`../internal/web/static/${name}`, import.meta.url), "utf8");
const markup = read("settings.html");
const code = read("settings.js").split("// Publishing uses a fresh revision")[1].split("// Typecho import is a two-step")[0];
const first = { path: "a/文章 & 日志.md", title: "文章", publishedAt: "2020-01-01T00:00:00Z", enabled: true, url: "/posts/abc", tags: ["测试"], categories: ["技术"] };
const second = { path: "随笔.md", title: "随笔", publishedAt: "2021-01-01T00:00:00Z", enabled: false, tags: ["随笔"] };

function setup(respond) {
  const elements = new Map(), calls = [];
  function el(tag, className, textContent = "") {
    return {
      tag, className, textContent, value: "", children: [], hidden: false,
      append(...children) { this.children.push(...children); },
      replaceChildren(...children) { this.children = children; },
      setAttribute() {}, removeAttribute() {}, focus() {},
      set innerHTML(_) { assert.fail("Article values must be inserted as text"); },
    };
  }
  const get = id => {
    assert.ok(markup.includes(`id="${id}"`), `missing ${id}`);
    if (!elements.has(id)) elements.set(id, el("div"));
    return elements.get(id);
  };
  const sandbox = {
    $: get, el, URL, URLSearchParams, Option: function (text, value) { return { text, value }; },
    gitBusy: false, systemBusy: false, typechoImportBusy: false, location: { search: "" },
    alertAt(id, message = "") { Object.assign(get(id), { textContent: message, hidden: !message }); },
    async request(path, options = {}) { calls.push({ path, ...options }); return respond(path, options); },
  };
  runInNewContext(code.slice(code.indexOf("\n")), sandbox);
  return { get, calls, load: sandbox.loadPublicationTable, rows: () => get("publication-rows").children };
}

test("the article title uses the editor deep link and taxonomy/search filters limit selection", async () => {
  const ui = setup(() => ({ articles: [first, second] }));
  await ui.load();
  const link = ui.rows()[0].children[1].children[0];
  const url = new URL(link.href, "https://example.invalid");
  assert.equal(url.pathname, "/admin/");
  assert.equal(url.searchParams.get("note"), first.path);
  assert.equal(url.searchParams.get("edit"), "1");
  assert.equal(link.target, undefined);
  ui.get("publication-tag").value = "测试";
  ui.get("publication-tag").onchange();
  assert.equal(ui.rows().length, 1);
  ui.get("publication-select-all").onchange({ target: { checked: true } });
  assert.equal(ui.get("publication-count").textContent, "1 篇 · 已选 1 篇");
  ui.get("publication-search").value = "不存在";
  ui.get("publication-search").oninput();
  assert.equal(ui.get("publication-batch-offline").disabled, true);
  assert.equal(ui.get("publication-empty").hidden, false);
});

test("batch actions affect eligible visible selections and report request failures", async () => {
  let fail = true;
  const ui = setup(path => {
    if (path.endsWith("/batch")) {
      if (fail) throw Error("远端冲突");
      return { changed: 1 };
    }
    return { articles: [first, second] };
  });
  await ui.load();
  ui.get("publication-select-all").onchange({ target: { checked: true } });
  await ui.get("publication-batch-offline").onclick();
  assert.deepEqual(JSON.parse(JSON.stringify(ui.calls[1].body)), { paths: [first.path], enabled: false });
  assert.equal(ui.get("publication-table-error").textContent, "远端冲突");
  assert.equal(ui.get("publication-table-status").textContent, "");
  fail = false;
  await ui.get("publication-batch-offline").onclick();
  assert.equal(ui.get("publication-table-status").textContent, "已下线 1 篇");
  assert.equal(ui.calls.at(-1).path, "/admin/api/publications");
});

test("table date editing does not send or change publication status", async () => {
  const ui = setup(() => ({ articles: [first] }));
  await ui.load();
  ui.rows()[0].children.at(-1).children[1].onclick();
  const form = ui.get("publication-table-date-form");
  assert.equal(form.hidden, false);
  const local = "2022-01-02T09:30:00";
  ui.get("publication-table-date").value = local;
  await form.onsubmit({ preventDefault() {} });
  const { path, method, body } = ui.calls[1];
  assert.equal(path, "/admin/api/publication");
  assert.equal(method, "PATCH");
  assert.deepEqual(JSON.parse(JSON.stringify(body)), { path: first.path, publishedAt: new Date(local).toISOString() });
  assert.equal(form.hidden, true);
});
