import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";
import { runInNewContext } from "node:vm";

const read = (name) => readFileSync(new URL(`../internal/web/static/${name}`, import.meta.url), "utf8");
const code = read("settings.js").split("// Public blog templates are independent of the workbench palette above.")[1].split("// General preferences")[0];
const markup = read("settings.html");
const themes = [
  { id: "default", name: "默认" },
  { id: "journal", name: "Journal", version: "1.2.0", description: "Editorial theme" },
  { id: "broken", name: "Broken", error: "模板有误" },
];

function setup(request) {
  const nodes = new Map();
  const element = () => ({
    textContent: "", value: "", hidden: true, disabled: false, children: [],
    replaceChildren() { this.children = []; this.value = ""; },
    append(child) { this.children.push(child); },
    removeAttribute(name) { delete this[name]; },
  });
  const get = (id) => {
    assert.ok(markup.includes(`id="${id}"`), `missing ${id}`);
    if (!nodes.has(id)) nodes.set(id, element());
    return nodes.get(id);
  };
  const load = runInNewContext(`${code}\nloadBlogThemes`, {
    $: get, URLSearchParams, request,
    alertAt(id, text = "") { Object.assign(get(id), { textContent: text, hidden: !text }); },
    el(_tag, _className, textContent) { return { ...element(), textContent }; },
  });
  return {
    load, get: (suffix) => get(`blog-template-${suffix}`),
    choose(id) { const select = get("blog-template-select"); select.value = id; select.onchange(); },
  };
}

test("theme selection updates real-content preview URLs but requires explicit activation", async () => {
  const calls = [];
  const post = "/posts/abc";
  const page = "/pages/about-me";
  const ui = setup(async (url, options) => {
    calls.push([url, options?.method, options?.body?.id]);
    const active = options?.body.id || "default";
    return { active, effective: active, themes, previews: { post, page } };
  });
  await ui.load();
  assert.equal(ui.get("enable").disabled, true);
  ui.choose("journal");
  assert.equal(calls.length, 1);
  assert.equal(ui.get("enable").disabled, false);
  assert.match(ui.get("description").textContent, /Editorial theme/);
  for (const [kind, path] of Object.entries({ home: "/", post, page })) {
    assert.match(markup, new RegExp(`id="blog-template-preview-${kind}" target="_blank" rel="noopener noreferrer"`));
    assert.equal(ui.get(`preview-${kind}`).hidden, false);
    const url = new URL(ui.get(`preview-${kind}`).href, "http://localhost");
    assert.equal(url.pathname, "/admin/theme-preview/journal");
    assert.equal(url.searchParams.get("path"), path);
  }
  await ui.get("enable").onclick();
  assert.deepEqual(calls, [["/admin/api/themes", undefined, undefined], ["/admin/api/themes", "POST", "journal"]]);
  assert.equal(ui.get("status").textContent, "当前主题：Journal");
  assert.equal(ui.get("enable").disabled, true);
});

test("broken themes cannot be previewed or activated; fallback can be made explicit", async () => {
  const calls = [];
  const ui = setup(async (_url, options) => {
    calls.push(options?.body?.id);
    return { active: options?.body.id || "broken", effective: "default", themes, previews: {} };
  });
  await ui.load();
  assert.match(ui.get("status").textContent, /Broken.*默认.*回退/);
  assert.equal(ui.get("error").textContent, "模板有误");
  assert.equal(ui.get("preview-home").hidden, true);
  assert.equal(ui.get("enable").disabled, true);
  ui.choose("default");
  assert.equal(ui.get("error").hidden, true);
  assert.equal(ui.get("enable").disabled, false);
  assert.equal(ui.get("preview-home").hidden, false);
  assert.equal(ui.get("preview-post").hidden, true);
  assert.equal(ui.get("preview-page").hidden, true);
  await ui.get("enable").onclick();
  assert.deepEqual(calls, [undefined, "default"]);
});

test("list errors are retryable and failed activation keeps the active theme and selection", async () => {
  let first = true;
  const ui = setup(async (_url, options) => {
    if (options?.method) throw new Error("启用失败");
    if (first) { first = false; throw new Error("读取失败"); }
    return { active: "default", effective: "default", themes };
  });
  await ui.load();
  assert.equal(ui.get("retry").hidden, false);
  assert.equal(ui.get("select").disabled, true);
  await ui.get("retry").onclick();
  ui.choose("journal");
  await ui.get("enable").onclick();
  assert.equal(ui.get("error").textContent, "启用失败");
  assert.equal(ui.get("select").value, "journal");
  assert.equal(ui.get("status").textContent, "当前主题：默认");
  assert.equal(ui.get("enable").disabled, false);
});
