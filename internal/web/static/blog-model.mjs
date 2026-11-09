// Pure validation for the blog settings panel. Kept free of DOM access so the
// same rules can be exercised with `node --test`; the server repeats every
// check and stays authoritative.
export const SLUG_PATTERN = /^[a-z0-9]+(?:-[a-z0-9]+)*$/;
export const MAX_PAGES = 12;

// Mirrors publication.Accents. A value outside this list is rejected by the
// server so the inline --accent custom property can never carry arbitrary CSS.
export const ACCENTS = ["#406c50", "#2f6f8f", "#8a5a3b", "#7a4b6d", "#3f5d7d", "#a0403f", "#5c6b2f", "#3a3a3a"];
export const THEMES = ["auto", "light", "dark"];

export function slugify(value) {
  return String(value || "")
    .toLocaleLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "")
    .slice(0, 64)
    .replace(/-+$/, "");
}

export function validateBlog(input) {
  const title = String(input?.title ?? "").trim();
  if (!title) return { ok: false, error: "站点标题不能为空" };
  const accent = String(input?.accent ?? "").toLocaleLowerCase();
  if (!ACCENTS.includes(accent)) return { ok: false, error: "请选择预设主题色" };
  const theme = String(input?.theme ?? "auto");
  if (!THEMES.includes(theme)) return { ok: false, error: "无效的配色模式" };
  const pages = Array.isArray(input?.pages) ? input.pages : [];
  if (pages.length > MAX_PAGES) return { ok: false, error: `最多 ${MAX_PAGES} 个独立页面` };
  const seen = new Set();
  for (const page of pages) {
    const slug = String(page?.slug ?? "").trim();
    if (!SLUG_PATTERN.test(slug) || slug.length > 64) return { ok: false, error: "短地址只能使用小写字母、数字和连字符，例如 about" };
    if (seen.has(slug)) return { ok: false, error: `短地址 ${slug} 重复` };
    seen.add(slug);
    if (!String(page?.title ?? "").trim()) return { ok: false, error: "每个独立页面都需要标题" };
  }
  return { ok: true, pages: pages.map((page) => ({ ...page, slug: String(page.slug).trim(), title: String(page.title).trim() })) };
}

// Unique slug suggestion for a new page, so adding several pages in a row does
// not immediately produce a duplicate that fails validation.
export function uniqueSlug(base, taken) {
  const used = new Set(taken || []);
  let candidate = slugify(base) || "page";
  if (!used.has(candidate)) return candidate;
  for (let suffix = 2; ; suffix++) {
    const next = `${candidate}-${suffix}`;
    if (!used.has(next)) return next;
  }
}