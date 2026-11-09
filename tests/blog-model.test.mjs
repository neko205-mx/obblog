import test from "node:test";
import assert from "node:assert/strict";
import { ACCENTS, THEMES, MAX_PAGES, slugify, uniqueSlug, validateBlog } from "../internal/web/static/blog-model.mjs";

test("slugify produces a safe standalone path segment", () => {
  assert.equal(slugify("About Me"), "about-me");
  assert.equal(slugify("  Hello, World!  "), "hello-world");
  assert.equal(slugify("中文标题"), "");
  assert.equal(slugify(""), "");
  assert.equal(slugify("---"), "");
  assert.ok(slugify("a".repeat(200)).length <= 64);
});

test("validateBlog accepts a complete configuration", () => {
  const result = validateBlog({
    title: "山间笔记",
    accent: ACCENTS[2],
    theme: "dark",
    pages: [{ slug: "about", title: "关于", source: "# hi" }],
  });
  assert.equal(result.ok, true);
  assert.deepEqual(result.pages[0].slug, "about");
});

test("validateBlog rejects values the server would refuse", () => {
  const base = { title: "t", accent: ACCENTS[0], theme: "auto" };
  assert.equal(validateBlog({ ...base, title: "   " }).ok, false);
  assert.equal(validateBlog({ ...base, accent: "#123456" }).ok, false);
  assert.equal(validateBlog({ ...base, accent: "red;background:url(x)" }).ok, false);
  assert.equal(validateBlog({ ...base, theme: "sepia" }).ok, false);
  assert.equal(validateBlog({ ...base, pages: [{ slug: "About Me", title: "t" }] }).ok, false);
  assert.equal(validateBlog({ ...base, pages: [{ slug: "a", title: "t" }, { slug: "a", title: "u" }] }).ok, false);
  assert.equal(validateBlog({ ...base, pages: [{ slug: "a", title: "" }] }).ok, false);
});

test("validateBlog enforces the page cap", () => {
  const pages = Array.from({ length: MAX_PAGES + 1 }, (_, index) => ({ slug: `p${index}`, title: "t" }));
  assert.equal(validateBlog({ title: "t", accent: ACCENTS[0], theme: "auto", pages }).ok, false);
});

test("validateBlog only accepts the published palette and themes", () => {
  assert.deepEqual(THEMES, ["auto", "light", "dark"]);
  for (const accent of ACCENTS) {
    assert.match(accent, /^#[0-9a-f]{6}$/);
  }
});

test("uniqueSlug avoids collisions when adding pages in a row", () => {
  assert.equal(uniqueSlug("about", []), "about");
  assert.equal(uniqueSlug("about", ["about"]), "about-2");
  assert.equal(uniqueSlug("about", ["about", "about-2"]), "about-3");
  assert.equal(uniqueSlug("中文", []), "page");
  assert.equal(uniqueSlug("", ["page"]), "page-2");
});