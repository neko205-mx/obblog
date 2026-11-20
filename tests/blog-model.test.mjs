import test from "node:test";
import assert from "node:assert/strict";
import { ACCENTS, THEMES, slugify, uniqueSlug, validateBlog, validatePage } from "../internal/web/static/blog-model.mjs";

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
  });
  assert.deepEqual(result, { ok: true });
});

test("validateBlog rejects values the server would refuse", () => {
  const base = { title: "t", accent: ACCENTS[0], theme: "auto" };
  assert.equal(validateBlog({ ...base, title: "   " }).ok, false);
  assert.equal(validateBlog({ ...base, accent: "#123456" }).ok, false);
  assert.equal(validateBlog({ ...base, accent: "red;background:url(x)" }).ok, false);
  assert.equal(validateBlog({ ...base, theme: "sepia" }).ok, false);
});

test("standalone page requires a title and URL-safe slug", () => {
  assert.equal(validatePage({ slug: "about-me", title: "关于我" }), null);
  assert.equal(validatePage({ slug: "about", title: " " }).field, "title");
  for (const slug of ["About Me", "../about", "关于我", "", "a".repeat(65)]) {
    assert.equal(validatePage({ slug, title: "关于我" }).field, "slug");
  }
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