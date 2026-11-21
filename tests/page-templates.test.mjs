import test from "node:test";
import assert from "node:assert/strict";
import { initPages } from "../internal/web/static/pages.js";

const friends = { id: "default/friends", label: "友情链接", description: "公开链接列表", title: "我的友链", slug: "friends", source: "- [朋友](https://example.test/)", format: "friend-links" };
const exercise = { id: "sample/featured", label: "专题", title: "专题页", slug: "featured", source: "## 专题", format: "custom" };

function setup(t, { catalog, detail, preview } = {}) {
  const elements = new Map();
  let focused;
  function element(tag) {
    return {
      tag, value: "", children: [], dataset: {}, attributes: {}, listeners: {}, classList: { remove() {} },
      focus() { focused = this; }, select() {},
      addEventListener(name, fn) { this.listeners[name] = fn; },
      setAttribute(key, value) { this.attributes[key] = value; },
      removeAttribute(key) { delete this.attributes[key]; },
      append(...children) { this.children.push(...children); },
      prepend(child) { this.children.unshift(child); },
      replaceChildren(...children) {
        this.children = children;
        delete this.html;
        if (tag === "page-template-select") this.value = children[0]?.value || "";
      },
      set innerHTML(html) { this.children = []; this.html = html; },
    };
  }
  const $ = id => {
    if (!elements.has(id)) elements.set(id, element(id));
    return elements.get(id);
  };
  $("page-template-chooser").hidden = $("editor-view").hidden = true;
  const calls = [];
  const fetch = async (url, opts) => {
    const body = JSON.parse(opts.body);
    calls.push([url, opts.method, body]);
    const data = url.endsWith("/preview") ? preview?.(body) || { html: "<p>safe</p>" }
      : { ...body, revision: "rev2", url: `/pages/${body.slug}` };
    return { ok: true, json: async () => data };
  };
  for (const [name, value] of Object.entries({
    document: { getElementById: $, querySelectorAll: () => [], createElement: element },
    window: { addEventListener() {}, confirm: () => true },
    location: { href: "http://localhost/admin/settings?tab=blog" }, navigator: { onLine: true }, fetch,
  })) {
    const original = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { configurable: true, value });
    t.after(() => original ? Object.defineProperty(globalThis, name, original) : delete globalThis[name]);
  }
  const pages = initPages({ request: async url => url.endsWith("/pages") ? catalog() : detail,
    toast: assert.fail, profileReady: () => true });
  pages.showTab(true);
  return { $, pages, calls, focused: () => focused };
}

const settle = () => new Promise(resolve => setImmediate(resolve));
const waitPreview = () => new Promise(resolve => setTimeout(resolve, 5));

test("template chooser is cancelable; choosing a starter creates an unsaved Markdown draft without affecting blank creation", async t => {
  const { $, pages, calls, focused } = setup(t, { catalog: () => ({
    templates: [friends, exercise], pages: [{ slug: "friends", title: "友情链接", url: "/pages/friends" }], limit: 10,
  }) });
  await settle();
  $("page-template-open").onclick();
  assert.equal($("page-template-open").attributes["aria-expanded"], "true");
  assert.equal($("page-template-description").textContent, friends.description);
  assert.equal(focused(), $("page-template-select"));
  $("page-template-cancel").onclick();
  assert.equal($("page-template-chooser").hidden, true);
  assert.equal($("editor-view").hidden, true);
  assert.equal(pages.dirty, false);
  assert.equal(focused(), $("page-template-open"));
  $("page-template-open").onclick();
  $("page-template-apply").onclick();
  assert.equal($("page-title").value, friends.title);
  assert.equal($("editor-path").value, "friends-2");
  assert.equal($("note-editor").value, friends.source);
  assert.equal($("page-template-marker").textContent, "模板：友情链接");
  assert.equal(pages.dirty, true);
  await waitPreview();
  assert.equal(calls[0][2].template, friends.id);
  assert.equal($("editor-preview").html, "<p>safe</p>");
  assert.equal(calls.some(([url]) => url === "/admin/api/pages"), false);
  await $("save-note").onclick();
  assert.deepEqual(calls[1], ["/admin/api/pages", "POST", { slug: "friends-2", title: friends.title, source: friends.source, template: friends.id }]);
  await $("close-editor").onclick();
  $("page-create").onclick();
  assert.equal($("note-editor").value, "");
  assert.equal($("page-template-marker").hidden, true);
  await waitPreview();
  assert.equal("template" in calls.at(-1)[2], false);
});

test("unavailable template stays on existing page and its warning is plain text", async t => {
  const page = { slug: "old", title: "原页面", source: "原内容", url: "/pages/old", revision: "rev1", template: "absent/old" };
  const warning = "<strong>模板已停用</strong>";
  const { $, calls } = setup(t, { detail: page, catalog: () => ({ templates: [friends], pages: [page], limit: 10 }),
    preview: () => ({ html: "<p>ordinary fallback</p>", templateWarning: warning }) });
  await settle();
  await $("page-list").children[0].children[1].onclick();
  assert.match($("page-template-marker").textContent, /absent\/old/);
  await waitPreview();
  assert.equal(calls[0][2].template, page.template);
  assert.equal($("editor-preview").html, "<p>ordinary fallback</p>");
  assert.equal($("page-preview-warning").textContent, warning);
  assert.equal($("page-preview-warning").hidden, false);
  $("page-title").value = "新标题";
  await $("save-note").onclick();
  assert.equal(calls[1][2].template, page.template);
  await $("close-editor").onclick();
});

test("custom template HTML can only be assigned to a sandboxed iframe", async t => {
  let custom = false;
  const fragment = "<html><script>parent.document.body.remove()</script></html>";
  const { $, pages } = setup(t, { catalog: () => ({ templates: custom ? [friends, exercise] : [friends], pages: [], limit: 10 }),
    preview: () => ({ html: "<p>must not be rendered</p>", sandboxHTML: fragment }) });
  await settle();
  assert.equal($("page-template-select").children.length, 1);
  pages.showTab(false);
  custom = true;
  pages.showTab(true);
  await settle();
  $("page-template-open").onclick();
  assert.equal($("page-template-select").children.length, 2);
  $("page-template-select").value = exercise.id;
  $("page-template-apply").onclick();
  await waitPreview();
  const preview = $("editor-preview");
  assert.equal(preview.html, undefined);
  assert.equal(preview.children[0].textContent, exercise.title);
  const frame = preview.children[1];
  assert.equal(frame.tag, "iframe");
  assert.deepEqual(frame.attributes, { sandbox: "" });
  assert.equal(frame.srcdoc, fragment);
  assert.equal($("page-preview-warning").hidden, true);
  await $("close-editor").onclick();
  assert.equal(preview.children.length, 0);
});
