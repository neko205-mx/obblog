import test from "node:test";
import assert from "node:assert/strict";
import { Draft, encodeDraft, normalizeForEditor, wikiSuggestions, indentSelection, imageFiles, pastedImageName, imageEmbed } from "../internal/web/static/editor-model.mjs";

test("clipboard image extraction ignores text and avoids duplicate file entries", () => {
  const image = { name: "image.png", type: "image/png" };
  const text = { name: "note.md", type: "text/plain" };
  assert.deepEqual(imageFiles({ items: [{ kind: "string" }, { kind: "file", getAsFile: () => image }], files: [image] }), [image]);
  assert.deepEqual(imageFiles({ files: [text, image, { name: "vector.SVG", type: "" }] }), [image, { name: "vector.SVG", type: "" }]);
  assert.deepEqual(imageFiles(null), []);
  assert.equal(pastedImageName(image, new Date(2026, 10, 8, 9, 5, 3)), "Pasted image 20261108090503.png");
  assert.equal(imageEmbed("图片 (1).png"), "![[图片 (1).png]]");
});

test("draft preserves YAML, BOM, CRLF and original bytes on a no-op", () => {
  const source = "\ufeff---\r\ncustom:\r\n  nested: true\r\n---\r\n# 标题\r\n";
  const draft = new Draft({ path: "A.md", source, revision: "version" });
  assert.equal(draft.dirty, false);
  assert.equal(draft.source, source);
  draft.text += "添加内容\n";
  assert.equal(draft.dirty, true);
  assert.equal(draft.source, source + "添加内容\r\n");
  assert.equal(encodeDraft(normalizeForEditor(source), source), source);
});

test("save acknowledgement advances revision without discarding newer input", () => {
  const draft = new Draft({ path: "A.md", source: "old", revision: "v1" });
  draft.text = "submitted";
  const submitted = draft.submission();
  draft.text = "newer input";
  draft.accept({ path: "A.md", revision: "v2" }, submitted);
  assert.equal(draft.text, "newer input");
  assert.equal(draft.dirty, true);
  assert.equal(draft.revision, "v2");
});

test("new notes remain unsaved until an acknowledged create", () => {
  const draft = new Draft({ path: "new.md", source: "", isNew: true });
  assert.equal(draft.dirty, true);
  const submitted = draft.submission("folder/new.md");
  draft.accept({ path: "folder/new.md", revision: "saved" }, submitted);
  assert.equal(draft.dirty, false);
  assert.equal(draft.isNew, false);
  assert.equal(draft.path, "folder/new.md");
});

test("double-link completion offers explicit paths and ignores completed links", () => {
  const notes = [{ path: "notes/中文.md" }, { path: "other/A.md" }];
  assert.equal(wikiSuggestions("[[中", 3, notes).items[0].insert, "notes/中文");
  assert.equal(wikiSuggestions("![[", 3, notes).start, 1);
  assert.equal(wikiSuggestions("[[中文]]", 6, notes), null);
  assert.equal(wikiSuggestions("[[A#章节", 6, notes), null);
  assert.equal(wikiSuggestions("[[旧\n新", 6, notes), null);
});

test("Tab indents selected lines without deleting their contents; Shift+Tab reverses it", () => {
  const text = "one\ntwo\nthree";
  const indent = indentSelection(text, 0, 7);
  const indented = text.slice(0, indent.from) + indent.insert + text.slice(indent.to);
  assert.equal(indented, "    one\n    two\nthree");
  const outdent = indentSelection(indented, indent.start, indent.end, true);
  assert.equal(indented.slice(0, outdent.from) + outdent.insert + indented.slice(outdent.to), text);
  const caret = indentSelection("hello", 2, 2);
  assert.equal(caret.insert, "    ");
  assert.equal(caret.start, 6);
});
