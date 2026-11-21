import { NoteEditor } from "./editor.js";
import { uniqueSlug, validatePage } from "./blog-model.mjs";

// A page draft belongs to the blog tab, not to the site-wide settings form.
export function initPages({ request, toast, profileReady }) {
  const $ = (id) => document.getElementById(id);
  const endpoint = "/admin/api/pages";
  let catalog, opening = false, deleting = false, loading, blogVisible = false;
  let saved;
  const pathInput = $("editor-path");
  const titleInput = $("page-title");
  const listStatus = $("page-list-status");
  const chooser = $("page-template-chooser");
  const chooserButton = $("page-template-open");
  const templateSelect = $("page-template-select");
  const templateDescription = $("page-template-description");
  const applyTemplateButton = $("page-template-apply");
  const detailURL = (slug) => `${endpoint}/${encodeURIComponent(slug)}`;
  const templates = () => catalog?.templates || [];
  const findTemplate = (id) => templates().find(template => template.id === id);
  const input = (source) => ({
    slug: pathInput.value.trim(), title: titleInput.value.trim(), source,
    ...(saved?.template ? { template: saved.template } : {}),
  });
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
      if (!await refresh()) throw new Error("页面列表刷新失败");
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
    const marker = $("page-template-marker");
    const template = saved?.template && findTemplate(saved.template);
    marker.hidden = !saved?.template;
    marker.textContent = template ? `模板：${template.label}`
      : saved?.template ? `模板不可用：${saved.template}` : "";
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
    for (const { title, slug, url, template } of entries) {
      const item = node("li");
      const info = node("div");
      const name = node("button", title, "page-edit-link");
      name.type = "button";
      name.onclick = () => open(slug);
      const label = node("div", undefined, "page-list-label");
      label.append(name);
      if (template) label.append(node("span", findTemplate(template)?.label || "模板不可用", "page-template-marker"));
      const link = node("a", url);
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener";
      info.append(label, link);
      const action = node("button", "编辑");
      action.type = "button";
      action.setAttribute("aria-label", `编辑 ${title}`);
      action.onclick = name.onclick;
      item.append(info, action);
      list.append(item);
    }
    const full = Boolean(catalog && entries.length >= catalog.limit);
    $("page-create").disabled = !canCreate();
    renderChooser();
    listStatus.textContent = !entries.length && catalog ? "还没有独立页面。" : full ? `已达到 ${catalog.limit} 个页面的上限。` : "";
  }

  function canCreate() {
    return Boolean(catalog && !opening && !editor.active && catalog.pages.length < catalog.limit);
  }

  function renderChooser() {
    const previous = templateSelect.value;
    const options = templates();
    templateSelect.replaceChildren(...options.map(template => {
      const option = node("option", template.label);
      option.value = template.id;
      return option;
    }));
    if (findTemplate(previous)) templateSelect.value = previous;
    chooserButton.disabled = !canCreate() || !options.length;
    if (chooserButton.disabled) setChooser(false);
    showTemplateDescription();
  }

  function showTemplateDescription() {
    const template = findTemplate(templateSelect.value);
    templateDescription.textContent = template?.description || "";
    templateDescription.hidden = !template?.description;
    applyTemplateButton.disabled = !canCreate() || Boolean(loading) || !template;
  }

  function setChooser(open, focus = false) {
    chooser.hidden = !open;
    chooserButton.setAttribute("aria-expanded", String(open));
    if (focus) (open ? templateSelect : chooserButton).focus();
  }

  async function refresh() {
    if (loading) return loading;
    listStatus.textContent = "正在读取页面…";
    loading = request(endpoint).then((data) => {
      catalog = data;
      renderList();
      if (editor.active) renderActions();
      return true;
    }).catch((error) => { listStatus.textContent = error.message; return false; })
      .finally(() => { loading = null; showTemplateDescription(); });
    showTemplateDescription();
    return loading;
  }

  function showEditor(value) {
    saved = value;
    setChooser(false);
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
    catch (error) { toast(error.message); await refresh(); }
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
    if (!canCreate()) return;
    showEditor({ slug: uniqueSlug("page", catalog.pages.map((page) => page.slug)), title: "新页面", source: "" });
  };
  chooserButton.onclick = () => setChooser(chooser.hidden, true);
  templateSelect.onchange = showTemplateDescription;
  $("page-template-cancel").onclick = () => setChooser(false, true);
  $("page-template-chooser").onkeydown = event => {
    if (event.key !== "Escape") return;
    event.preventDefault();
    setChooser(false, true);
  };
  applyTemplateButton.onclick = () => {
    const template = findTemplate(templateSelect.value);
    if (!canCreate() || loading || !template) return;
    const taken = catalog.pages.map(page => page.slug);
    const suggested = uniqueSlug(template.slug, taken);
    // Preserve room for a collision suffix even when the starter uses all 64 characters.
    const slug = suggested.length <= 64 ? suggested : uniqueSlug(template.slug.slice(0, 61), taken);
    showEditor({ slug, title: template.title, source: template.source, template: template.id });
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
      await refresh();
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
    showTab(isBlog) {
      if (isBlog && !blogVisible && !editor.active) void refresh();
      blogVisible = isBlog;
    },
  };
}
