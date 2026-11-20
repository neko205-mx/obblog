import { NoteEditor } from "./editor.js";
import { uniqueSlug, validatePage } from "./blog-model.mjs";

// A page draft belongs to the blog tab, not to the site-wide settings form.
export function initPages({ request, toast, profileReady }) {
  const $ = (id) => document.getElementById(id);
  const endpoint = "/admin/api/pages";
  let catalog, opening = false, deleting = false, loading;
  let saved;
  const pathInput = $("editor-path");
  const titleInput = $("page-title");
  const listStatus = $("page-list-status");
  const detailURL = (slug) => `${endpoint}/${encodeURIComponent(slug)}`;
  const input = (source) => ({ slug: pathInput.value.trim(), title: titleInput.value.trim(), source });
  const editor = new NoteEditor({
    notify: toast,
    notes: () => catalog?.linkableNotes || [],
    page: {
      input,
      dirtyMetadata: () => pathInput.value.trim() !== saved?.slug || titleInput.value.trim() !== saved?.title,
      validate() {
        const invalid = validatePage(input(""));
        if (!invalid) return true;
        editor.status(invalid.error, "error");
        (invalid.field === "slug" ? pathInput : titleInput).focus();
        return false;
      },
    },
    onClose: () => close(),
    onChange: () => renderActions(),
    async onSaved(page) {
      saved = page;
      titleInput.value = page.title;
      renderActions();
      editor.schedulePreview(0);
      if (!await refresh(true)) throw new Error("页面列表刷新失败");
    },
  });

  function renderActions() {
    const exists = Boolean(saved?.url);
    $("page-editor-title").textContent = exists ? "编辑独立页面" : "新建独立页面";
    $("page-public-link").hidden = !exists;
    if (exists) $("page-public-link").href = saved.url;
    $("page-delete").hidden = !exists;
    $("page-delete").disabled = editor.busy || deleting || !navigator.onLine;
    $("close-editor").disabled = editor.busy || deleting;
  }

  function node(tag, text, className) {
    const result = document.createElement(tag);
    if (text !== undefined) result.textContent = text;
    if (className) result.className = className;
    return result;
  }

  function renderList() {
    const list = $("page-list");
    list.replaceChildren();
    const entries = catalog?.pages || [];
    for (const { title, slug, url } of entries) {
      const item = node("li");
      const info = node("div");
      const name = node("button", title, "page-edit-link");
      name.type = "button";
      name.onclick = () => open(slug);
      const link = node("a", url);
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener";
      info.append(name, link);
      const action = node("button", "编辑");
      action.type = "button";
      action.setAttribute("aria-label", `编辑 ${title}`);
      action.onclick = name.onclick;
      item.append(info, action);
      list.append(item);
    }
    const full = Boolean(catalog && entries.length >= catalog.limit);
    $("page-create").disabled = !catalog || full || opening;
    listStatus.textContent = !entries.length && catalog ? "还没有独立页面。" : full ? `已达到 ${catalog.limit} 个页面的上限。` : "";
  }

  async function refresh(force = false) {
    if (loading) return loading;
    if (catalog && !force) return true;
    listStatus.textContent = "正在读取页面…";
    loading = request(endpoint).then((data) => {
      catalog = data;
      renderList();
      return true;
    }).catch((error) => { listStatus.textContent = error.message; return false; })
      .finally(() => { loading = null; });
    return loading;
  }

  function showEditor(value) {
    saved = value;
    $("standalone-pages").hidden = $("blog-form").hidden = true;
    $("editor-view").hidden = false;
    titleInput.value = value.title;
    renderActions();
    editor.open({ ...value, path: value.slug }, !value.url);
    titleInput.focus();
    if (!value.url) titleInput.select();
  }

  async function open(slug) {
    if (opening || editor.active) return;
    opening = true;
    renderList();
    listStatus.textContent = "正在打开页面…";
    try { showEditor(await request(detailURL(slug))); }
    catch (error) { toast(error.message); await refresh(true); }
    finally { opening = false; renderList(); }
  }

  async function close(discard = false) {
    if (!discard && (editor.busy || deleting || (editor.dirty && !window.confirm("页面有未保存的修改，是否放弃？")))) return;
    editor.close();
    saved = undefined;
    $("editor-view").hidden = true;
    $("standalone-pages").hidden = false;
    $("blog-form").hidden = !profileReady();
    renderList();
    $("page-list-title").focus();
  }

  $("page-create").onclick = () => {
    if (!catalog || opening || editor.active || catalog.pages.length >= catalog.limit) return;
    showEditor({ slug: uniqueSlug("page", catalog.pages.map((page) => page.slug)), title: "新页面", source: "" });
  };
  $("page-delete").onclick = async () => {
    if (!saved?.url || editor.busy || deleting) return;
    if (!window.confirm(`删除「${saved.title}」后，公开页面与导航链接都会移除。确定删除？`)) return;
    deleting = true;
    editor.saving = true;
    editor.status("正在删除…");
    try {
      await request(detailURL(saved.slug), { method: "DELETE", body: { revision: saved.revision } });
      await close(true);
      await refresh(true);
    } catch (error) {
      editor.status(error.message, "error");
    } finally {
      deleting = editor.saving = false;
      if (editor.active) editor.status($("editor-status").textContent, $("editor-status").dataset.state);
    }
  };

  return {
    get dirty() { return editor.dirty; },
    get busy() { return editor.busy || opening || deleting; },
    showTab(isBlog) { if (isBlog && !editor.active) void refresh(); },
  };
}
