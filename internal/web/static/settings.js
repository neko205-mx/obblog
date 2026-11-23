import { validateBlog } from "./blog-model.mjs";
import { initPages } from "./pages.js";

const $ = (id) => document.getElementById(id);
const endpoint = "/admin/api/settings";
let context, systemBusy = false, systemDirty = false, leaving = false, directory;

async function request(url, { body, method = "GET" } = {}) {
  const response = await fetch(url, { cache: "no-store", method,
    ...(body !== undefined && { headers: { "Content-Type": "application/json" }, body: JSON.stringify(body) }),
  });
  const data = await response.json().catch(() => null);
  if (!response.ok || data === null) {
    throw new Error(response.status === 401 ? "会话已过期，请重新登录" : data?.error || `请求失败（${response.status}）`);
  }
  return data;
}

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function alertAt(id, message = "") {
  $(id).textContent = message;
  $(id).hidden = !message;
}

// System form and directory picker.
function error(message = "") {
  $("settings-error").textContent = message;
  $("settings-error").hidden = !message;
}

function mode() { return document.querySelector('input[name="mode"]:checked').value; }
function updateSource() {
  const source = mode();
  $("existing-fields").hidden = source !== "existing";
  $("clone-fields").hidden = source !== "clone";
  $("vault-path").required = source === "existing";
  $("vault-path").disabled = source !== "existing";
  $("repository-url").required = source === "clone";
  $("repository-url").disabled = source !== "clone";
  $("repository-branch").disabled = source !== "clone";
  $("credentials").hidden = source === "empty";
}
document.querySelectorAll('input[name="mode"]').forEach((input) => input.onchange = updateSource);
$("git-token").oninput = () => { if ($("git-token").value) $("clear-token").checked = false; };

$("settings-form").onsubmit = async (event) => {
  event.preventDefault();
  if (!context?.managed || workInProgress()) return;
  error();
  const password = $("new-password").value;
  const confirmation = $("confirm-password").value;
  const length = new TextEncoder().encode(password).length;
  if (password && (length < 12 || length > 1024)) { error("密码需要 12 至 1024 字节"); $("new-password").focus(); return; }
  if (password !== confirmation) { error("两次密码输入不一致"); $("confirm-password").focus(); return; }
  const input = { mode: mode(), vault: $("vault-path").value.trim(), repository: $("repository-url").value.trim(), branch: $("repository-branch").value.trim(), username: $("git-username").value.trim(), token: $("git-token").value.trim(), clearToken: $("clear-token").checked, password, confirmation };
  if (input.mode === "clone" && !input.token && context.tokenConfigured && !input.clearToken) { error("更换仓库请重新填写 Token，公开仓库请勾选清除已保存 Token"); $("git-token").focus(); return; }
  if ((blogDirty || pages.dirty) && !window.confirm("保存系统设置会退出登录，是否放弃未保存的博客或页面修改？")) return;
  systemBusy = true;
  const controls = [...$("settings-form").querySelectorAll("input,button")].map((control) => [control, control.disabled]);
  controls.forEach(([control]) => { control.disabled = true; });
  $("save-settings").disabled = true;
  $("save-settings").textContent = input.mode === "clone" ? "克隆中…" : "保存中…";
  try {
    const result = await request(endpoint, { method: "POST", body: input });
    $("new-password").value = $("confirm-password").value = $("git-token").value = "";
    systemBusy = false;
    leaving = true;
    location.assign(result.redirect);
  } catch (e) { error(e.message); }
  finally {
    systemBusy = false;
    controls.forEach(([control, disabled]) => { control.disabled = disabled; });
    $("save-settings").disabled = false;
    $("save-settings").textContent = "保存系统设置";
  }
};

let directoryRequest = 0;
async function browse(path = "") {
  const id = ++directoryRequest;
  directory = null;
  $("directory-select").disabled = $("directory-parent").disabled = true;
  $("directory-error").textContent = "正在读取…";
  try {
    const data = await request(`${endpoint}/directories?${new URLSearchParams({ path })}`);
    if (id !== directoryRequest) return;
    directory = data;
    $("directory-select").disabled = $("directory-parent").disabled = false;
    $("directory-location").value = data.path;
    $("directory-list").replaceChildren();
    for (const name of data.directories) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = name;
      button.onclick = () => browse(`${data.path.replace(/\/$/, "")}/${name}`);
      $("directory-list").append(button);
    }
    if (!data.directories.length) $("directory-list").textContent = "无子目录";
    $("directory-error").textContent = "";
  } catch (e) { if (id === directoryRequest) $("directory-error").textContent = e.message; }
}
function toggleDirectory(visible) {
  $("directory-browser").hidden = !visible;
  $("browse-directory").setAttribute("aria-expanded", String(visible));
}
$("browse-directory").onclick = () => {
  const open = $("directory-browser").hidden;
  toggleDirectory(open);
  if (open) {
    browse($("vault-path").value.trim());
    $("directory-location").focus();
  }
};
$("directory-close").onclick = () => { toggleDirectory(false); $("browse-directory").focus(); };
$("directory-open").onclick = () => browse($("directory-location").value.trim());
$("directory-location").onkeydown = (event) => {
  if (event.key === "Enter" && !event.isComposing) { event.preventDefault(); $("directory-open").click(); }
};
$("directory-parent").onclick = () => { if (directory) browse(directory.parent); };
$("directory-select").onclick = () => {
  if (!directory) return;
  $("vault-path").value = directory.path;
  systemDirty = true;
  toggleDirectory(false);
  $("vault-path").focus();
};

$("settings-form").oninput = (event) => { if (!event.target.closest("#directory-browser")) systemDirty = true; };

// Shared appearance and blog configuration.
let blogConfig = null;
let siteBusy = false, blogDirty = false, siteLoading = null;

function blogError(message = "") {
  const node = $("blog-error");
  node.textContent = message;
  node.hidden = !message;
}

// Swatches show the color itself plus its name, because a hex value alone does
// not tell an author what the page will look like.
const ACCENT_NAMES = {
  "#406c50": "松绿", "#2f6f8f": "雾蓝", "#8a5a3b": "赭石",
  "#7a4b6d": "紫藤", "#3f5d7d": "石青", "#a0403f": "朱砂",
  "#5c6b2f": "苔绿", "#3a3a3a": "墨灰",
};

function renderAccentSwatches(values, selected) {
  const container = $("blog-accent");
  container.replaceChildren();
  const current = selected || values[0] || "";
  for (const value of values) {
    const label = document.createElement("label");
    label.className = "accent-swatch";
    label.style.setProperty("--swatch", value);
    label.title = `${ACCENT_NAMES[value] || value} ${value}`;
    const radio = document.createElement("input");
    radio.type = "radio";
    radio.name = "blog-accent";
    radio.value = value;
    radio.checked = value === current;
    radio.onchange = () => { blogPreview(); saveAppearance(); };
    label.append(radio, el("span", "accent-dot"), el("span", "accent-name", ACCENT_NAMES[value] || value));
    container.append(label);
  }
}

function selectedAccent() {
  return document.querySelector('input[name="blog-accent"]:checked')?.value || "";
}

function selectedTheme() {
  return document.querySelector('input[name="blog-theme"]:checked')?.value || "auto";
}

function blogPreview() {
  const accent = selectedAccent();
  const title = blogConfig?.title || "未命名博客";
  const author = blogConfig?.author;
  const preview = $("blog-preview");
  preview.style.setProperty("--preview-accent", accent);
  $("blog-preview-text").textContent = author ? `${title} · ${author}` : title;
}

function renderBlog(config) {
  blogDirty = false;
  $("blog-title").value = config.title || "";
  $("blog-description").value = config.description || "";
  $("blog-author").value = config.author || "";
  $("blog-footer").value = config.footer || "";
  $("blog-state").textContent = config.vault || "";
}

// Send only persisted fields, never GET-only metadata.
function blogSettingsPayload(config) {
  return {
    title: config.title ?? "",
    description: config.description ?? "",
    author: config.author ?? "",
    footer: config.footer ?? "",
    accent: config.accent ?? "",
    theme: config.theme ?? "auto",
  };
}

function blogPayload() {
  return {
    title: $("blog-title").value.trim(),
    description: $("blog-description").value.trim(),
    author: $("blog-author").value.trim(),
    footer: $("blog-footer").value.trim(),
    accent: blogConfig.accent,
    theme: blogConfig.theme,
  };
}

$("blog-form").oninput = () => { blogDirty = true; };
for (const input of document.querySelectorAll('input[name="blog-theme"]')) input.onchange = () => { blogPreview(); saveAppearance(); };
$("blog-form").onsubmit = async (event) => {
  event.preventDefault();
  if (siteBusy || !blogConfig) return;
  const payload = blogPayload();
  const checked = validateBlog(payload);
  if (!checked.ok) { blogError(checked.error); return; }
  setSiteBusy(true);
  blogError();
  $("blog-state").textContent = "保存中…";
  try {
    const saved = await request("/admin/api/blog", { method: "POST", body: payload });
    blogConfig = saved;
    renderBlog(saved);
    blogPreview();
    $("blog-state").textContent = "已保存";
  } catch (error) { blogError(error.message); $("blog-state").textContent = ""; }
  finally { setSiteBusy(false); }
};

function renderAppearance() {
  renderAccentSwatches(blogConfig.accents || [], blogConfig.accent);
  for (const input of document.querySelectorAll('input[name="blog-theme"]')) input.checked = input.value === blogConfig.theme;
  blogPreview();
}

function setSiteBusy(busy) {
  siteBusy = busy;
  $("accent-field").disabled = $("theme-field").disabled = busy;
  for (const control of $("blog-form").querySelectorAll("input, textarea, button")) control.disabled = busy;
}

function loadSiteSettings() {
  if (blogConfig || siteLoading) return siteLoading;
  $("blog-state").textContent = $("appearance-state").textContent = "读取中…";
  alertAt("appearance-error");
  blogError();
  // Coalesce rapid tab switches; never refill an already edited form.
  siteLoading = request("/admin/api/blog").then((config) => {
    blogConfig = config;
    renderBlog(config);
    renderAppearance();
    if ($("editor-view").hidden) $("blog-form").hidden = false;
    setSiteBusy(false);
    $("appearance-state").textContent = "公开博客";
  }).catch((error) => {
    alertAt("appearance-error", error.message);
    blogError(error.message);
    $("blog-state").textContent = $("appearance-state").textContent = "读取失败";
  }).finally(() => { siteLoading = null; });
  return siteLoading;
}

async function saveAppearance() {
  if (siteBusy || !blogConfig) return;
  const body = blogSettingsPayload({ ...blogConfig, accent: selectedAccent(), theme: selectedTheme() });
  setSiteBusy(true);
  alertAt("appearance-error");
  $("appearance-state").textContent = "保存中…";
  try {
    blogConfig = await request("/admin/api/blog", { method: "POST", body });
    $("appearance-state").textContent = "已保存 · 公开博客";
  } catch (error) {
    // Roll back only the appearance controls, not a draft on the blog tab.
    renderAppearance();
    alertAt("appearance-error", error.message);
    $("appearance-state").textContent = "保存失败";
  } finally { setSiteBusy(false); }
}

// Public blog templates are independent of the workbench palette above.
const themeEndpoint = "/admin/api/themes";
const defaultTheme = "default";
let themeCatalog, themeLoading = false, themeSaving = false;

function renderBlogThemeSelection() {
  const select = $("blog-template-select");
  const theme = themeCatalog?.themes.find(({ id }) => id === select.value);
  const usable = Boolean(theme && !theme.error);
  select.disabled = !themeCatalog?.themes.length || themeLoading || themeSaving;
  $("blog-template-enable").disabled = !usable || themeLoading || themeSaving || theme.id === themeCatalog.active;
  $("blog-template-enable").textContent = themeSaving ? "启用中…" : "启用主题";
  alertAt("blog-template-description", [theme?.version, theme?.description].filter(Boolean).join(" · "));
  for (const [kind, path] of Object.entries({ home: "/", post: themeCatalog?.previews?.post, page: themeCatalog?.previews?.page })) {
    const link = $(`blog-template-preview-${kind}`);
    link.hidden = !usable || !path || themeLoading || themeSaving;
    if (link.hidden) link.removeAttribute("href");
    else link.href = `/admin/theme-preview/${encodeURIComponent(theme.id)}?${new URLSearchParams({ path })}`;
  }
}

function setBlogThemes(result) {
  themeCatalog = { ...result, active: result.active || defaultTheme, effective: result.effective || defaultTheme };
  const select = $("blog-template-select");
  const { active, effective, themes } = themeCatalog;
  const label = (id) => themes.find((theme) => theme.id === id)?.name || id;
  select.replaceChildren();
  for (const theme of themes) {
    const state = theme.error ? "（不可用）" : theme.id === effective ? "（生效中）" : "";
    const option = el("option", "", `${theme.name || theme.id}${state}`);
    option.value = theme.id;
    select.append(option);
  }
  select.value = themes.some(({ id }) => id === active) ? active : effective;
  $("blog-template-status").textContent = active === effective
    ? `当前主题：${label(effective)}`
    : `已配置：${label(active)}；实际生效：${label(effective)}（回退）`;
  alertAt("blog-template-error", themes.find(({ id }) => id === select.value)?.error || "");
  $("blog-template-retry").hidden = true;
  renderBlogThemeSelection();
}

async function loadBlogThemes() {
  if (themeLoading || themeSaving) return;
  themeLoading = true;
  $("blog-template-status").textContent = "读取中…";
  $("blog-template-retry").hidden = true;
  alertAt("blog-template-error");
  renderBlogThemeSelection();
  try { setBlogThemes(await request(themeEndpoint)); }
  catch (error) {
    themeCatalog = null;
    $("blog-template-select").replaceChildren();
    $("blog-template-status").textContent = "无法读取主题";
    alertAt("blog-template-error", error.message);
    $("blog-template-retry").hidden = false;
  } finally {
    themeLoading = false;
    renderBlogThemeSelection();
  }
}

$("blog-template-select").onchange = () => {
  const theme = themeCatalog?.themes.find(({ id }) => id === $("blog-template-select").value);
  alertAt("blog-template-error", theme?.error || "");
  renderBlogThemeSelection();
};
$("blog-template-retry").onclick = loadBlogThemes;
$("blog-template-enable").onclick = async () => {
  const id = $("blog-template-select").value;
  const theme = themeCatalog?.themes.find((item) => item.id === id);
  if (!theme || theme.error || themeLoading || themeSaving || id === themeCatalog.active) return;
  themeSaving = true;
  alertAt("blog-template-error");
  renderBlogThemeSelection();
  try { setBlogThemes(await request(themeEndpoint, { method: "POST", body: { id } })); }
  catch (error) { alertAt("blog-template-error", error.message); }
  finally { themeSaving = false; renderBlogThemeSelection(); }
};

// General preferences come from the shared server-side registry.
let preferences, preferencesBusy = false;
function renderPreferences(list, focusKey) {
  const container = $("preference-list");
  container.replaceChildren();
  if (!list.length) container.append(el("p", "field-note", "暂无可调整选项"));
  const groups = new Map();
  let restoreFocus;
  for (const pref of list) {
    if (!groups.has(pref.section)) {
      const group = el("div", "pref-group");
      group.append(el("h3", "pref-group-title", pref.section));
      groups.set(pref.section, group);
      container.append(group);
    }
    const row = el("label", "pref-row");
    const text = el("span", "pref-text");
    text.append(el("strong", "", pref.label));
    if (pref.note) text.append(el("small", "", pref.note));
    if (pref.locked) text.append(el("small", "", pref.locked));
    const toggle = el("input", "pref-toggle");
    toggle.type = "checkbox";
    toggle.setAttribute("role", "switch");
    toggle.checked = Boolean(pref.value);
    toggle.disabled = Boolean(pref.locked);
    toggle.onchange = () => savePreference(pref.key, toggle.checked);
    if (pref.key === focusKey) restoreFocus = toggle;
    row.append(text, toggle);
    groups.get(pref.section).append(row);
  }
  if (currentTab === "general") restoreFocus?.focus();
}

async function loadPreferences() {
  if (preferences || preferencesBusy) return;
  preferencesBusy = true;
  alertAt("prefs-error");
  $("prefs-state").textContent = "读取中…";
  try {
    preferences = await request("/admin/api/preferences");
    renderPreferences(preferences);
    $("prefs-state").textContent = "";
  } catch (error) {
    alertAt("prefs-error", error.message);
    $("prefs-state").textContent = "读取失败";
  } finally { preferencesBusy = false; }
}

async function savePreference(key, value) {
  if (preferencesBusy) return;
  preferencesBusy = true;
  for (const toggle of $("preference-list").querySelectorAll("input")) toggle.disabled = true;
  alertAt("prefs-error");
  $("prefs-state").textContent = "保存中…";
  try {
    preferences = await request("/admin/api/preferences", { method: "POST", body: { values: { [key]: value } } });
    $("prefs-state").textContent = "已保存";
  } catch (error) {
    alertAt("prefs-error", error.message);
    $("prefs-state").textContent = "保存失败";
  } finally {
    preferencesBusy = false;
    renderPreferences(preferences, key);
  }
}

// Git actions remain explicit and report their result in this tab.
let gitBusy = false, gitStatus;
const gitLabels = { disabled: "本地目录", current: "已同步", dirty: "本地修改待处理", offline: "远端不可用", ahead: "本地提交待推送", changed: "远端已变化", blocked: "仓库状态异常" };
function renderGit() {
  if (gitStatus) {
    $("git-state").textContent = gitLabels[gitStatus.state] || gitStatus.state;
    $("git-branch").textContent = gitStatus.branch || "—";
    $("git-head").textContent = gitStatus.head?.slice(0, 12) || "—";
    $("git-remote").textContent = gitStatus.remoteHead?.slice(0, 12) || "—";
  }
  $("git-check").disabled = gitBusy;
  $("git-pull").disabled = gitBusy || gitStatus?.state !== "changed";
  $("git-push").disabled = gitBusy || !gitStatus?.canPush;
}

async function gitOperation(action = "check") {
  if (gitBusy || publicationBusy || systemBusy || typechoImportBusy) return;
  gitBusy = true;
  renderGit();
  $("git-message").textContent = action === "check" ? "正在核对远端…" : "同步中…";
  try {
    const checking = action === "check";
    gitStatus = await request(`/admin/api/git${checking ? "" : `/${action}`}`, { method: checking ? "GET" : "POST" });
    $("git-message").textContent = gitStatus.warning || gitStatus.message;
  } catch (error) {
    $("git-message").textContent = error.message;
    gitStatus = null;
    $("git-state").textContent = "请重新检查";
  } finally {
    gitBusy = false;
    renderGit();
    if (action !== "check") {
      // A pull may change both available paths and the revision of the active note.
      notesLoaded = false;
      clearPublication();
      if (currentTab === "publication") loadPublicationTab();
    }
  }
}
$("git-check").onclick = () => gitOperation();
$("git-pull").onclick = () => gitOperation("pull");
$("git-push").onclick = () => gitOperation("push");

// Publishing uses a fresh revision and Git HEAD for the selected note.
let notesLoaded = false, notesLoading = false, publicationBusy = false;
let publicationRequest = 0, publicationNote, publicationRecord;
const noteQuery = (path) => `?${new URLSearchParams({ path })}`;
function renderPublication() {
  $("publication-controls").hidden = !publicationNote;
  $("publication-note").disabled = publicationBusy || notesLoading || !notesLoaded;
  $("publication-refresh").disabled = publicationBusy;
  $("publication-enabled").disabled = publicationBusy || !publicationNote;
  $("publication-enabled").checked = Boolean(publicationRecord?.enabled);
  const metadata = $("publication-metadata");
  metadata.hidden = !publicationRecord?.enabled;
  const labels = [];
  if (publicationRecord?.title) labels.push(`标题：${publicationRecord.title}`);
  if (publicationRecord?.tags?.length) labels.push(`标签：${publicationRecord.tags.join("、")}`);
  if (publicationRecord?.categories?.length) labels.push(`分类：${publicationRecord.categories.join("、")}`);
  metadata.textContent = labels.join(" · ");
  $("publication-date").disabled = $("publication-date-save").disabled = publicationBusy || !publicationRecord?.id;
  const date = publicationRecord?.publishedAt ? new Date(publicationRecord.publishedAt) : null;
  $("publication-date").value = date ? new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19) : "";
  const link = $("publication-link");
  link.hidden = !(publicationRecord?.enabled && publicationRecord.url);
  if (link.hidden) link.removeAttribute("href");
  else link.href = publicationRecord.url;
}

function clearPublication() {
  publicationRequest++;
  publicationNote = publicationRecord = null;
  renderPublication();
}

async function selectPublication(path) {
  clearPublication();
  const id = publicationRequest;
  $("publication-status").textContent = path ? "读取发布状态…" : "";
  if (!path) return;
  try {
    const [note, record] = await Promise.all([
      request(`/admin/api/note${noteQuery(path)}`), request(`/admin/api/publication${noteQuery(path)}`),
    ]);
    if (id !== publicationRequest) return;
    publicationNote = note;
    publicationRecord = record;
    $("publication-status").textContent = "";
    renderPublication();
  } catch (error) { if (id === publicationRequest) $("publication-status").textContent = error.message; }
}

async function loadPublicationTab() {
  if (notesLoading || gitBusy || publicationBusy) return;
  const selected = new URLSearchParams(location.search).get("note") || "";
  if (notesLoaded) {
    const select = $("publication-note");
    if (select.value !== selected) {
      select.value = selected;
      if (select.selectedIndex < 0) select.value = "";
      selectPublication(select.value);
    }
    return;
  }
  notesLoading = true;
  renderPublication();
  $("publication-status").textContent = "读取笔记列表…";
  try {
    const { notes } = await request("/admin/api/vault");
    const select = $("publication-note");
    select.replaceChildren(new Option("请选择笔记", ""));
    for (const { path } of notes) select.add(new Option(path, path));
    select.value = new URLSearchParams(location.search).get("note") || "";
    if (select.selectedIndex < 0) select.value = "";
    notesLoaded = true;
    if (notes.length) void selectPublication(select.value);
    else $("publication-status").textContent = "知识库中还没有笔记";
  } catch (error) { $("publication-status").textContent = error.message; }
  finally { notesLoading = false; renderPublication(); }
}

$("publication-note").onchange = () => {
  const path = $("publication-note").value;
  const url = new URL(location.href);
  if (path) url.searchParams.set("note", path); else url.searchParams.delete("note");
  history.replaceState(null, "", url);
  renderTabLinks();
  selectPublication(path);
};

async function updatePublication(enabled, date) {
  if (publicationBusy || gitBusy || systemBusy || typechoImportBusy || !publicationNote) { renderPublication(); return; }
  const { path, revision, gitHead } = publicationNote;
  publicationBusy = true;
  renderPublication();
  $("publication-status").textContent = "保存中…";
  try {
    publicationRecord = await request("/admin/api/publication", { method: "PUT", body: { path, enabled, publishedAt: date, revision, gitHead } });
    $("publication-status").textContent = date ? "发布时间已保存" : enabled ? "已发布" : "已取消发布";
  } catch (error) { $("publication-status").textContent = error.message; }
  finally { publicationBusy = false; renderPublication(); }
}
$("publication-enabled").onchange = (event) => updatePublication(event.target.checked);
$("publication-date-form").onsubmit = (event) => {
  event.preventDefault();
  if (!publicationRecord?.id) return;
  const date = new Date($("publication-date").value);
  if (!Number.isFinite(date.getTime()) || date.getTime() > Date.now() + 60000) {
    $("publication-status").textContent = "发布时间不能晚于当前时间";
    return;
  }
  updatePublication(publicationRecord.enabled, date.toISOString());
};
$("publication-refresh").onclick = async () => {
  if (publicationBusy || gitBusy || notesLoading || systemBusy || typechoImportBusy) return;
  const path = $("publication-note").value;
  clearPublication();
  publicationBusy = true;
  renderPublication();
  $("publication-status").textContent = "更新中…";
  let message = "公开副本已更新";
  try { await request("/admin/api/publications/refresh", { method: "POST" }); }
  catch (error) { message = error.message; }
  finally {
    if (path) await selectPublication(path);
    publicationBusy = false;
    renderPublication();
    if (!$("publication-status").textContent) $("publication-status").textContent = message;
  }
};

// Typecho import is a two-step, explicitly submitted multipart upload.
const typechoImportURL = "/admin/api/import/typecho/";
const maxTypechoImportBytes = 32 * 1024 * 1024;
const typechoStatuses = { ready: "待导入", existing: "已存在", conflict: "路径冲突", imported: "已导入" };
const typechoSkipLabels = {
  nonPost: "非文章（含独立页面）", nonPublic: "未公开或受密码保护", invalidDate: "日期无效或在未来",
  nonMarkdown: "非 Markdown", invalidData: "数据不完整", tooLarge: "正文超出大小限制",
};
const typechoDate = new Intl.DateTimeFormat("zh-CN", { dateStyle: "medium", timeStyle: "short", timeZone: "Asia/Shanghai" });
const typechoFile = $("typecho-import-file");
let typechoImportBusy = false, typechoReviewed = null;

function typechoFileError(file) {
  if (!file) return "请选择 Typecho 的 .sql 备份。";
  if (!/\.sql$/i.test(file.name)) return "仅支持 .sql 备份文件。";
  if (file.size === 0) return "所选备份为空。";
  if (file.size > maxTypechoImportBytes) return "备份不能超过 32 MiB。";
  return "";
}

function updateTypechoControls() {
  const file = typechoFile.files[0];
  typechoFile.disabled = typechoImportBusy;
  $("typecho-import-preview").disabled = typechoImportBusy || Boolean(typechoFileError(file));
  $("typecho-import-apply").disabled = typechoImportBusy || !typechoReviewed || typechoReviewed.file !== file;
  $("typecho-import-form").setAttribute("aria-busy", String(typechoImportBusy));
}

function hideTypechoResult() {
  $("typecho-import-result").hidden = true;
  $("typecho-import-posts").replaceChildren();
  $("typecho-import-summary").textContent = "";
}

async function requestTypecho(action, file, checksum) {
  const body = new FormData();
  body.append("file", file);
  if (action === "apply") body.append("checksum", checksum);
  const response = await fetch(typechoImportURL + action, { method: "POST", cache: "no-store", body });
  const data = await response.json().catch(() => null);
  if (!response.ok) {
    throw new Error(response.status === 401 ? "会话已过期，请重新登录" : data?.error || data?.message || `导入请求失败（${response.status}）`);
  }
  if (!Array.isArray(data?.posts) || !["ready", "existing", "conflicts", "skipped"].every(key => Number.isSafeInteger(data.summary?.[key]) && data.summary[key] >= 0)) {
    throw new Error("服务器没有返回有效的导入结果，请重新预览。");
  }
  return data;
}

function safeTypechoPostURL(value) {
  if (typeof value !== "string" || !value) return null;
  try {
    const url = new URL(value, location.origin);
    if (url.origin === location.origin && !url.username && !url.password && /^\/posts\/[a-z0-9_-]+$/i.test(url.pathname)) return url.pathname;
  } catch { /* Ignore malformed or non-public article links. */ }
  return null;
}

function renderTypechoResult(data, applied) {
  const { ready, existing, conflicts, skipped } = data.summary;
  const visibleStatuses = applied ? ["imported", "existing"] : ["ready", "existing", "conflict"];
  const posts = data.posts.filter(post => post && visibleStatuses.includes(post.status));
  const imported = Number.isSafeInteger(data.summary.imported) && data.summary.imported >= 0
    ? data.summary.imported : posts.filter(post => post.status === "imported").length;
  $("typecho-import-result-title").textContent = applied ? "导入结果" : "导入检查结果";
  $("typecho-import-summary").textContent = [
    applied ? `已导入 ${imported} 篇` : `待导入 ${ready} 篇`,
    `已存在 ${existing} 篇`, `冲突 ${conflicts} 篇`, `跳过 ${skipped} 条`,
  ].join(" · ");
  const skippedSummary = [];
  for (const [key, label] of Object.entries(typechoSkipLabels)) {
    const count = data.skippedReasons?.[key];
    if (Number.isSafeInteger(count) && count > 0) skippedSummary.push(`${label} ${count} 条`);
  }
  const skippedNode = $("typecho-import-skipped");
  skippedNode.textContent = skippedSummary.length ? `跳过原因：${skippedSummary.join("、")}` : "";
  skippedNode.hidden = !skippedSummary.length;
  const list = $("typecho-import-posts");
  list.replaceChildren();
  for (const post of posts) {
    const row = el("li", "typecho-import-post");
    const heading = el("div", "typecho-import-post-heading");
    heading.append(el("strong", "", post.title || "无标题"), el("span", "mode-badge", typechoStatuses[post.status]));
    row.append(heading);
    const metadata = el("div", "typecho-import-post-meta");
    if (typeof post.publishedAt === "string" && Number.isFinite(Date.parse(post.publishedAt))) {
      const date = el("time", "", typechoDate.format(new Date(post.publishedAt)));
      date.dateTime = post.publishedAt;
      metadata.append(date);
    }
    if (typeof post.path === "string" && post.path) metadata.append(el("code", "", post.path));
    else if (typeof post.slug === "string" && post.slug) metadata.append(el("code", "", post.slug));
    for (const [key, label] of [["tags", "标签"], ["categories", "分类"]]) {
      if (!Array.isArray(post[key])) continue;
      const values = post[key].filter(value => typeof value === "string" && value);
      if (values.length) metadata.append(el("span", "", `${label}：${values.join("、")}`));
    }
    const url = ["existing", "imported"].includes(post.status) ? safeTypechoPostURL(post.url) : null;
    if (url) {
      const link = el("a", "", "查看文章 ↗");
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      metadata.append(link);
    }
    row.append(metadata);
    list.append(row);
  }
  list.hidden = posts.length === 0;
  $("typecho-import-result").hidden = false;
}

typechoFile.onchange = () => {
  typechoReviewed = null;
  hideTypechoResult();
  $("typecho-import-status").textContent = "";
  alertAt("typecho-import-error", typechoFile.files[0] ? typechoFileError(typechoFile.files[0]) : "");
  updateTypechoControls();
};

$("typecho-import-form").onsubmit = async (event) => {
  event.preventDefault();
  if (typechoImportBusy) return;
  const file = typechoFile.files[0];
  const invalid = typechoFileError(file);
  if (invalid) { alertAt("typecho-import-error", invalid); return; }
  typechoReviewed = null;
  hideTypechoResult();
  alertAt("typecho-import-error");
  $("typecho-import-status").textContent = "正在上传并检查备份…";
  typechoImportBusy = true;
  updateTypechoControls();
  try {
    const data = await requestTypecho("preview", file);
    if (typechoFile.files[0] !== file) return;
    renderTypechoResult(data, false);
    const blocked = data.blocked || (data.summary.conflicts ? "存在路径冲突，未写入任何笔记。请解决冲突后重新预览。" : "");
    const canImport = data.summary.ready > 0 && data.summary.conflicts === 0 && !blocked;
    if (canImport && typeof data.checksum === "string" && data.checksum.trim()) typechoReviewed = { file, checksum: data.checksum };
    alertAt("typecho-import-error", blocked || (canImport && !typechoReviewed ? "服务器未返回校验值，请重新预览。" : ""));
    $("typecho-import-status").textContent = typechoReviewed ? "预览完成，确认列表后可导入并发布。" : "预览完成，未写入知识库。";
  } catch (error) {
    alertAt("typecho-import-error", error.message);
    $("typecho-import-status").textContent = "";
  } finally {
    typechoImportBusy = false;
    updateTypechoControls();
  }
};

$("typecho-import-apply").onclick = async () => {
  if (typechoImportBusy || !typechoReviewed || typechoReviewed.file !== typechoFile.files[0]) return;
  if (workInProgress()) { alertAt("typecho-import-error", "后台正在保存或同步，请稍候再确认导入。"); return; }
  const { file, checksum } = typechoReviewed;
  // Any apply attempt consumes the review. A lost response may still have
  // completed the import; the administrator must preview again before retrying.
  typechoReviewed = null;
  alertAt("typecho-import-error");
  $("typecho-import-status").textContent = "正在导入并发布，请勿离开此页…";
  typechoImportBusy = true;
  updateTypechoControls();
  let refresh = false;
  try {
    const data = await requestTypecho("apply", file, checksum);
    renderTypechoResult(data, true);
    if (data.blocked) alertAt("typecho-import-error", data.blocked);
    $("typecho-import-status").textContent = data.message || (data.blocked ? "导入已阻止。" : "导入完成。");
    refresh = !data.blocked;
    if (refresh) typechoFile.value = "";
  } catch (error) {
    hideTypechoResult();
    alertAt("typecho-import-error", `${error.message} 请重新预览以确认当前状态。`);
    $("typecho-import-status").textContent = "";
  } finally {
    typechoImportBusy = false;
    updateTypechoControls();
  }
  if (refresh) {
    notesLoaded = false;
    if (currentTab === "publication") loadPublicationTab();
  }
};
updateTypechoControls();

// Version and updates now live under System, not a separate Tools tab.
let versionBusy = false, versionLoaded = false;
function renderVersion(current) {
  $("version-current").textContent = current.version === "dev"
    ? "开发版本" : current.version;
  if (current.commit && current.commit !== "unknown") $("version-current").textContent += ` (${current.commit.slice(0, 7)})`;
}
async function loadVersion() {
  if (versionLoaded) return;
  versionLoaded = true;
  try { renderVersion(await request("/admin/api/version")); }
  catch { versionLoaded = false; }
}
$("version-check").onclick = async () => {
  if (versionBusy) return;
  versionBusy = $("version-check").disabled = true;
  $("version-install").hidden = $("version-link").hidden = $("version-notes-wrap").hidden = true;
  $("version-state").textContent = "检查中…";
  $("version-message").textContent = "";
  $("version-latest").textContent = "—";
  try {
    const status = await request("/admin/api/update");
    renderVersion(status.current);
    $("version-latest").textContent = status.latest || "—";
    if (status.url) {
      const url = new URL(status.url, location.href);
      if (url.protocol === "https:" && url.hostname === "github.com") {
        $("version-link").href = url;
        $("version-link").hidden = false;
      }
    }
    $("version-notes").textContent = status.notes || "";
    $("version-notes-wrap").hidden = !status.notes;
    $("version-state").textContent = status.available ? "有新版本" : status.error ? "检查失败" : "已是最新";
    $("version-install").hidden = !status.available || !context.canInstallUpdate;
    $("version-message").textContent = status.error || (status.available ? context.canInstallUpdate ? "安装后会短暂重启服务。" : "当前运行模式不支持页面内安装。" : "");
  } catch (error) {
    $("version-state").textContent = "检查失败";
    $("version-message").textContent = error.message;
  } finally { versionBusy = $("version-check").disabled = false; }
};
$("version-install").onclick = async () => {
  if (versionBusy || !canLeave() || !window.confirm("安装更新会重启服务，是否继续？")) return;
  versionBusy = $("version-install").disabled = $("version-check").disabled = true;
  $("version-state").textContent = "更新中…";
  $("version-message").textContent = "正在下载并校验…";
  try {
    const result = await request("/admin/api/update/install", { method: "POST" });
    await request("/admin/api/update/restart", { method: "POST" });
    $("version-message").textContent = `${result.version} 已校验，正在重启…`;
    // Sessions are held in memory; a restarted process will ask for login.
    const deadline = Date.now() + 30000;
    while (Date.now() < deadline) {
      await new Promise((resolve) => setTimeout(resolve, 1400));
      try {
        const res = await fetch("/admin/api/version", { cache: "no-store", signal: AbortSignal.timeout(2000) });
        if (res.status === 401 || (res.ok && (await res.json()).version === result.version)) {
          leaving = true;
          location.reload();
          return;
        }
      } catch { /* Listener may be unavailable during restart. */ }
    }
    throw new Error("在 30 秒内未确认新版本已启动，请检查服务日志");
  } catch (error) {
    $("version-state").textContent = "更新未完成";
    $("version-message").textContent = error.message;
  } finally { versionBusy = $("version-install").disabled = $("version-check").disabled = false; }
};

// Primary tabs change the address and history, not the page or its drafts.
const pages = initPages({ request, toast: (message) => alertAt("settings-notice", message), profileReady: () => Boolean(blogConfig) });
const tabs = [...document.querySelectorAll('.settings-tabs [role="tab"]')];
let currentTab;
function tabKey(tab) { return tab.id.slice(4); }
function selectedTab() {
  const params = new URLSearchParams(location.search);
  if (context?.problem) return "system";
  const key = params.get("tab");
  return tabs.some((tab) => tabKey(tab) === key) ? key : "appearance";
}
function tabURL(key) {
  const url = new URL(location.href);
  url.searchParams.set("tab", key);
  url.hash = "";
  return url;
}
function renderTabLinks() { for (const tab of tabs) tab.href = tabURL(tabKey(tab)); }
function showTab(key) {
  currentTab = key;
  for (const tab of tabs) {
    const active = tabKey(tab) === key;
    tab.setAttribute("aria-selected", String(active));
    tab.tabIndex = active ? 0 : -1;
    $(tab.getAttribute("aria-controls")).hidden = !active;
  }
  renderTabLinks();
  if (key === "appearance" || key === "blog") loadSiteSettings();
  if (key === "appearance" && !themeCatalog) loadBlogThemes();
  pages.showTab(key === "blog");
  if (key === "general") loadPreferences();
  if (key === "publication") loadPublicationTab();
  if (key === "system" && !context?.problem) loadVersion();
}
function navigateTab(tab) {
  const key = tabKey(tab);
  if (key !== currentTab || location.hash) history.pushState(null, "", tabURL(key));
  showTab(key);
}
for (const tab of tabs) tab.onclick = (event) => {
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  event.preventDefault();
  if (context) navigateTab(tab);
};
document.querySelector(".settings-tabs").onkeydown = (event) => {
  const active = tabs.filter((tab) => !tab.hidden);
  const index = active.indexOf(event.target);
  if (index < 0 || !["ArrowLeft", "ArrowRight", "Home", "End"].includes(event.key) || !context) return;
  event.preventDefault();
  const next = event.key === "Home" ? 0 : event.key === "End" ? active.length - 1
    : (index + (event.key === "ArrowRight" ? 1 : -1) + active.length) % active.length;
  active[next].focus();
  navigateTab(active[next]);
};
window.addEventListener("popstate", () => { if (context) showTab(selectedTab()); });

function workInProgress() { return systemBusy || siteBusy || themeSaving || preferencesBusy || gitBusy || publicationBusy || typechoImportBusy || versionBusy || pages.busy; }
function canLeave() {
  if (workInProgress()) { alertAt("settings-notice", "正在保存或同步，请稍候"); return false; }
  return !(systemDirty || blogDirty || pages.dirty) || window.confirm("还有未保存的设置，是否放弃修改？");
}
window.addEventListener("pageshow", (event) => { if (event.persisted) leaving = false; });
window.addEventListener("beforeunload", (event) => {
  if (!leaving && (systemDirty || blogDirty || pages.dirty || workInProgress())) { event.preventDefault(); event.returnValue = ""; }
});
$("settings-return").onclick = (event) => {
  if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey) return;
  if (!canLeave()) event.preventDefault();
  else leaving = true;
};
$("settings-logout").onclick = async () => {
  if (!canLeave()) return;
  try { await request("/admin/api/logout", { method: "POST" }); leaving = true; location.assign("/admin/login"); }
  catch (error) { alertAt("settings-notice", error.message); }
};
$("end-test-button").onclick = async () => {
  if (!canLeave() || !window.confirm("结束测试会删除此次测试的数据，是否继续？")) return;
  $("end-test-button").disabled = true;
  try { await request("/admin/api/test/stop", { method: "POST" }); leaving = true; location.replace("about:blank"); }
  catch (error) { alertAt("settings-notice", error.message); $("end-test-button").disabled = false; }
};

async function initialize() {
  const returnPath = new URLSearchParams(location.search).get("return");
  try {
    const url = new URL(returnPath || "/admin/", location.origin);
    if (url.origin === location.origin && url.pathname === "/admin/") $("settings-return").href = url;
  } catch { /* Invalid return URL; use the workspace link. */ }
  try {
    context = await request(endpoint);
    $("end-test-button").hidden = !context.testMode;
    if (context.managed) {
      $("vault-path").value = context.vault;
      $("git-username").value = context.username || "";
      $("save-mode").textContent = context.git ? "Git 同步" : "本地目录";
      $("token-state").textContent = context.tokenConfigured ? "已设置" : "未设置";
      $("git-token").placeholder = context.tokenConfigured ? "留空保留，填写替换" : "公开仓库可留空";
      $("clear-token-label").hidden = !context.tokenConfigured;
      updateSource();
      $("settings-form").hidden = $("save-settings").disabled = false;
    } else {
      $("settings-loading").textContent = "使用命令行管理知识库路径和管理员密码。";
    }
    if (context.problem) {
      for (const tab of tabs) tab.hidden = tabKey(tab) !== "system";
      $("system-version").hidden = true;
      $("settings-return").hidden = true;
      alertAt("settings-notice", context.problem);
    }
    showTab(selectedTab());
  } catch (error) { alertAt("settings-notice", error.message); }
  finally { $("settings-loading").hidden = context?.managed !== false; }
}
initialize();
