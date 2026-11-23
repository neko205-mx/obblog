import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";

const read = (name) => readFileSync(new URL(`../internal/web/static/${name}`, import.meta.url), "utf8");
const code = read("settings.js").split("// Typecho import is a two-step, explicitly submitted multipart upload.")[1].split("// Version and updates now live under System")[0];
const markup = read("settings.html");
const origin = "https://blog.test";
const sql = () => new File(["synthetic SQL fixture"], "typecho.SQL");
const summary = (counts = {}) => ({ ready: 1, existing: 0, conflicts: 0, skipped: 0, ...counts });
const preview = (changes = {}) => ({ checksum: "preview-digest", summary: summary(), posts: [{ id: 1, title: "示例文章", status: "ready" }], ...changes });

function setup(handle) {
  const elements = new Map(), calls = [];
  let refreshes = 0;
  function makeElement(tag = "div", className = "", text = "") {
    return {
      tag, className, textContent: text, hidden: false, disabled: false, attributes: {}, children: [],
      append(...children) { this.children.push(...children); },
      replaceChildren() { this.children = []; },
      setAttribute(name, value) { this.attributes[name] = value; },
      set innerHTML(_) { assert.fail("import data must not use innerHTML"); },
    };
  }
  const get = id => {
    assert.ok(markup.includes(`id="${id}"`), `missing ${id}`);
    if (!elements.has(id)) {
      const el = makeElement();
      if (id === "typecho-import-file") {
        el.files = [];
        Object.defineProperty(el, "value", { set(value) { assert.equal(value, ""); el.files = []; } });
      }
      elements.set(id, el);
    }
    return elements.get(id);
  };
  runInNewContext(code, {
    $: get, URL, FormData, location: { origin }, el: makeElement, notesLoaded: true, currentTab: "publication",
    loadPublicationTab() { refreshes++; }, workInProgress: () => false,
    alertAt(id, text = "") { Object.assign(get(id), { textContent: text, hidden: !text }); },
    fetch(url, options) { calls.push({ url, ...options }); return handle(url, options); },
  });
  const node = name => get(`typecho-import-${name}`);
  return {
    calls, node, refreshes: () => refreshes,
    choose(file) { node("file").files = file ? [file] : []; node("file").onchange(); },
    preview() { return node("form").onsubmit({ preventDefault() {} }); },
    apply() { return node("apply").onclick(); },
  };
}

const responses = (...datasets) => async () => {
  const data = datasets.shift();
  return data instanceof Response ? data : Response.json(data);
};

const descendants = list => list.children.flatMap(node => [node, ...descendants(node)]);

test("Typecho details remain within the publication tab, with six primary tabs", () => {
  assert.equal((markup.match(/role="tab"/g) || []).length, 6);
  const publication = markup.split('id="settings-publication"')[1].split('id="settings-system"')[0];
  assert.match(publication, /<details[^>]*>\s*<summary>Typecho 导入<\/summary>/);
  assert.match(publication, /type="file" accept="\.sql"/);
});

test("preview and apply require separate user actions and send only multipart file and checksum", async () => {
  const ui = setup(responses(preview(), {
    message: "导入完成", summary: summary({ ready: 0, imported: 1 }),
    posts: [{ title: "示例文章", status: "imported", url: "/posts/abc-123" }],
  }));
  const file = sql();
  ui.choose(file);
  assert.equal(ui.calls.length, 0);
  await ui.apply();
  assert.equal(ui.calls.length, 0);
  await ui.preview();
  assert.equal(ui.calls.length, 1);
  assert.equal(ui.calls[0].url, "/admin/api/import/typecho/preview");
  assert.equal(ui.calls[0].method, "POST");
  assert.equal(ui.calls[0].headers, undefined);
  assert.deepEqual([...ui.calls[0].body.keys()], ["file"]);
  assert.equal(await ui.calls[0].body.get("file").text(), await file.text());
  assert.equal(ui.node("apply").disabled, false);
  await ui.apply();
  assert.equal(ui.calls[1].url, "/admin/api/import/typecho/apply");
  assert.deepEqual([...ui.calls[1].body.keys()], ["file", "checksum"]);
  assert.equal(ui.calls[1].body.get("checksum"), "preview-digest");
  assert.equal(await ui.calls[1].body.get("file").text(), await file.text());
  assert.equal(ui.refreshes(), 1);
  assert.match(ui.node("summary").textContent, /已导入 1 篇/);
  assert.equal(ui.node("apply").disabled, true);
  assert.equal(descendants(ui.node("posts")).find(el => el.tag === "a").href, "/posts/abc-123");
  await ui.apply();
  assert.equal(ui.calls.length, 2);
});

test("validation, conflicts, no ready records, blocking and changing file prevent apply", async () => {
  const ui = setup(responses(
    preview({ summary: summary({ conflicts: 1 }) }),
    preview({ blocked: "当前知识库不可写" }),
    preview({ summary: summary({ ready: 0 }) }),
    preview({ checksum: "" }), preview(),
  ));
  for (const file of [new File(["x"], "backup.zip"), new File([], "empty.sql"), { name: "big.sql", size: 32 * 1024 * 1024 + 1 }]) {
    ui.choose(file);
    assert.equal(ui.node("preview").disabled, true);
    await ui.preview();
  }
  assert.equal(ui.calls.length, 0);
  ui.choose(sql());
  for (let n = 1; n <= 4; n++) {
    await ui.preview();
    assert.equal(ui.node("apply").disabled, true);
    await ui.apply();
    assert.equal(ui.calls.length, n);
  }
  await ui.preview();
  assert.equal(ui.node("apply").disabled, false);
  ui.choose(sql());
  assert.equal(ui.node("result").hidden, true);
  assert.equal(ui.node("apply").disabled, true);
  await ui.apply();
  assert.equal(ui.calls.length, 5);
});

test("review uses text only, ignores unknown statuses and filters unsafe links", async () => {
  const malicious = '<img src=x onerror=alert(1)>';
  const ui = setup(responses(preview({ skippedReasons: { nonPublic: 7, UNKNOWN_PRIVATE_REASON: 9 }, posts: [
    { title: malicious, path: malicious, tags: [malicious], status: "existing", url: "javascript:alert(1)" },
    { title: "其他站点", status: "existing", url: "https://elsewhere.test/posts/abc" },
    { title: "后台地址", status: "existing", url: "/admin/" },
    { title: "可查看", status: "existing", url: "https://blog.test/posts/abc" },
    { title: "不应展示的记录", status: "skipped" },
  ] })));
  ui.choose(sql());
  await ui.preview();
  const nodes = descendants(ui.node("posts"));
  assert.ok(nodes.some(el => el.textContent === malicious));
  assert.ok(!nodes.some(el => el.textContent === "不应展示的记录"));
  assert.equal(ui.node("skipped").textContent, "跳过原因：未公开或受密码保护 7 条");
  assert.deepEqual(nodes.filter(el => el.tag === "a").map(el => [el.href, el.rel]), [["/posts/abc", "noopener noreferrer"]]);
});

test("JSON apply errors invalidate the checksum and require another preview", async () => {
  const ui = setup(responses(preview(), Response.json({ error: "导入计划已过期" }, { status: 409 })));
  ui.choose(sql());
  await ui.preview();
  await ui.apply();
  assert.match(ui.node("error").textContent, /导入计划已过期.*重新预览/);
  assert.equal(ui.node("apply").disabled, true);
  await ui.apply();
  assert.equal(ui.calls.length, 2);
  assert.equal(ui.refreshes(), 0);
});
