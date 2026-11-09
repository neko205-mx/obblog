import test from "node:test";
import assert from "node:assert/strict";
import { NoteEditor } from "../internal/web/static/editor.js";
import { Draft } from "../internal/web/static/editor-model.mjs";

function setup(t, request) {
  const elements = new Map();
  function element(id) {
    if (!elements.has(id)) elements.set(id, {
      value: "", listeners: {}, dataset: {}, classList: { add() {}, remove() {} },
      addEventListener(name, fn) { this.listeners[name] = fn; },
      removeAttribute() {}, focus() {},
      setRangeText(insert, start, end) {
        this.value = this.value.slice(0, start) + insert + this.value.slice(end);
        this.selectionStart = this.selectionEnd = start + insert.length;
      },
      setSelectionRange(start, end) { this.selectionStart = start; this.selectionEnd = end; },
    });
    return elements.get(id);
  }
  for (const [name, value] of Object.entries({
    document: { getElementById: element, querySelectorAll: () => [] },
    window: { addEventListener() {} }, navigator: { onLine: true }, fetch: request,
  })) {
    const previous = Object.getOwnPropertyDescriptor(globalThis, name);
    Object.defineProperty(globalThis, name, { configurable: true, value });
    t.after(() => previous ? Object.defineProperty(globalThis, name, previous) : delete globalThis[name]);
  }
  const messages = [];
  const editor = new NoteEditor({ notify: message => messages.push(message), gitMode: () => true });
  editor.active = true;
  editor.draft = new Draft({ path: "nested/note.md", source: "before SELECT after", revision: "note-v1" });
  editor.gitHead = "head-v1";
  editor.textarea.value = editor.draft.text;
  editor.textarea.setSelectionRange(7, 13);
  editor.status = () => { editor.textarea.readOnly = editor.busy; };
  editor.input = () => { editor.draft.text = editor.textarea.value; };
  editor.schedulePreview = () => {};
  return { editor, messages, element };
}

const png = () => new File(["fixture"], "photo.png", { type: "image/png" });
const response = data => ({ ok: true, json: async () => data });

test("multiple uploads replace the selection once and advance Git HEAD before saving", async t => {
  const calls = [];
  const { editor } = setup(t, async (url, options) => {
    calls.push(options.body.get("gitHead"));
    return response({ path: `photo${calls.length}.png`, gitHead: `head-v${calls.length + 1}`, synced: true });
  });
  await editor.uploadImages([png(), png()]);
  assert.deepEqual(calls, ["head-v1", "head-v2"]);
  assert.equal(editor.draft.text, "before ![[photo1.png]]\n![[photo2.png]]\n after");
  assert.equal(editor.gitHead, "head-v3");
  assert.equal(editor.draft.revision, "note-v1");
  assert.equal(editor.dirty, true);
  assert.equal(editor.busy, false);
});

test("upload blocks navigation and save; subsequent network failure retains prior embeds", async t => {
  let release, count = 0;
  const { editor } = setup(t, async () => {
    if (++count === 2) throw new Error("network failed");
    await new Promise(resolve => { release = resolve; });
    return response({ path: "uploaded.png", gitHead: "head-v2", synced: true });
  });
  const pending = editor.uploadImages([png(), png()]);
  assert.equal(editor.busy, true);
  assert.equal(await editor.save(), false);
  assert.equal(await editor.mayLeave(), false);
  release();
  await pending;
  assert.equal(editor.draft.text, "before ![[uploaded.png]]\n after");
  assert.equal(editor.gitHead, "head-v2");
  assert.equal(editor.busy, false);
});

test("push warning inserts the saved image but stops the remaining batch", async t => {
  let count = 0;
  const { editor, messages } = setup(t, async () => {
    count++;
    return response({ path: "pending.png", gitHead: "head-v2", synced: false, warning: "push failed" });
  });
  await editor.uploadImages([png(), png()]);
  assert.equal(count, 1);
  assert.match(editor.draft.text, /!\[\[pending.png\]\]/);
  assert.equal(editor.warning, "push failed");
  assert.ok(messages.includes("push failed"));
});

test("paste and drop events route images without intercepting ordinary text paste", async t => {
  const { editor } = setup(t, async () => {});
  const uploads = [];
  editor.uploadImages = (files, pasted) => uploads.push({ files, pasted });
  const image = png();
  let prevented = 0;
  editor.textarea.listeners.paste({ clipboardData: { files: [] }, preventDefault() { prevented++; } });
  assert.equal(prevented, 0);
  editor.textarea.listeners.paste({ clipboardData: { items: [{ kind: "file", getAsFile: () => image }] }, preventDefault() { prevented++; } });
  editor.textarea.listeners.drop({ dataTransfer: { types: ["Files"], files: [image] }, preventDefault() { prevented++; } });
  assert.equal(prevented, 2);
  assert.deepEqual(uploads, [{ files: [image], pasted: true }, { files: [image], pasted: undefined }]);
});

test("pasted image gets an Obsidian-style name; offline uploads do not write", async t => {
  let count = 0;
  const { editor } = setup(t, async (url, options) => {
    count++;
    assert.match(options.body.get("file").name, /^Pasted image \d{14}\.png$/);
    return response({ path: "pasted.png", gitHead: "head-v2" });
  });
  await editor.uploadImages([png()], true);
  navigator.onLine = false;
  await editor.uploadImages([png()]);
  assert.equal(count, 1);
});
