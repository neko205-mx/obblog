import { GraphView } from "./graph.js";
import { NoteEditor } from "./editor.js";

const $ = (selector) => document.querySelector(selector);
const state = { vault: null, tabs: [], current: null, note: null, source: false, expanded: new Set(), request: 0, view: "note", graphTab: false };
let toastTimer;
const graphView = new GraphView({
  openNote: (path) => openNote(path),
  notify: (message) => toast(message),
  scopeChanged: (local) => history.replaceState(null, "", graphLocation(local)),
});
const editor = new NoteEditor({
  onChange: () => {
    renderTabs();
    if (state.view === "edit") {
      $("#note-status").textContent = `${editor.dirty ? "未保存" : editor.warning ? "本地已保存 · Git 待处理" : editor.synced ? "已推送" : "已载入"} · Markdown`;
      document.title = `${editor.dirty ? "* " : ""}${displayName(state.current)} · 编辑 · OB Vault`;
    }
  },
  onSaved: async (saved, source, previousPath) => {
    state.current = saved.path;
    state.tabs = [...new Set(state.tabs.map((path) => path === previousPath ? saved.path : path))];
    state.note = { ...state.note, path: saved.path, title: displayName(saved.path), source, revision: saved.revision, modified: saved.modified, gitHead: saved.gitHead };
    history.replaceState(null, "", noteURL(saved.path));
    editor.location = location.href;
    graphView.invalidate();
    applySnapshot(await api("/admin/api/vault"));
  },
  onClose: () => exitEditor(),
  onUploaded: async (saved) => {
    if (state.note && saved.gitHead) state.note.gitHead = saved.gitHead;
    graphView.invalidate();
    applySnapshot(await api("/admin/api/vault"));
  },
  notify: (message) => toast(message),
  notes: () => state.vault?.notes || [],
  gitMode: () => state.vault?.saveMode === "git",
});

function el(tag, className, text) {
  const node = document.createElement(tag);
  if (className) node.className = className;
  if (text !== undefined) node.textContent = text;
  return node;
}

function icon(name) {
  const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg");
  const use = document.createElementNS("http://www.w3.org/2000/svg", "use");
  use.setAttribute("href", `#i-${name}`);
  svg.setAttribute("aria-hidden", "true");
  svg.append(use);
  return svg;
}

function toast(message) {
  clearTimeout(toastTimer);
  $("#toast").textContent = message;
  $("#toast").hidden = false;
  toastTimer = setTimeout(() => { $("#toast").hidden = true; }, 4500);
}

async function api(url, options = {}) {
  const response = await fetch(url, { cache: "no-store", ...options });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `请求失败（${response.status}）`);
  return data;
}

function displayName(path) { return path.split("/").pop().replace(/\.md$/i, ""); }
function assetURL(path) { return `/admin/api/asset?${new URLSearchParams({ path })}`; }
function noteURL(path, fragment = "") { return `/admin/?${new URLSearchParams({ note: path })}${fragment}`; }

function setPane(open) {
  document.body.classList.toggle("pane-open", open);
  document.body.classList.toggle("pane-hidden", !open);
  $("#pane-toggle").setAttribute("aria-expanded", String(open));
}

function reveal(path) {
  const parts = path.split("/");
  for (let i = 1; i < parts.length; i++) state.expanded.add(parts.slice(0, i).join("/"));
}

function renderTree() {
  const root = $("#file-tree");
  root.replaceChildren();
  if (!state.vault) return;
  const query = $("#file-search").value.trim().toLocaleLowerCase();
  if (query) {
    const matches = state.vault.notes.filter((n) => n.path.toLocaleLowerCase().includes(query));
    root.append(el("div", "search-caption", `${matches.length} 篇匹配的笔记`));
    for (const note of matches) {
      const button = el("button", `search-result${state.current === note.path ? " active" : ""}`);
      button.append(icon("file"), el("strong", "", displayName(note.path)), el("small", "", note.path));
      button.onclick = () => openNote(note.path);
      root.append(button);
    }
    if (!matches.length) root.append(el("p", "tree-message", "没有匹配的笔记"));
    return;
  }
  function add(entries, parent) {
    for (const entry of entries) {
      const item = el("div", "tree-item");
      const button = el("button", `tree-row${state.current === entry.path ? " active" : ""}`);
      button.title = entry.path;
      if (entry.kind === "folder") {
        const expanded = state.expanded.has(entry.path);
        button.classList.add("folder-row");
        button.setAttribute("aria-expanded", String(expanded));
        const chevron = icon("chevron");
        chevron.classList.add("folder-chevron");
        if (expanded) chevron.classList.add("expanded");
        button.append(chevron, icon("folder"), el("span", "tree-name", entry.name));
        button.onclick = () => {
          if (state.expanded.has(entry.path)) state.expanded.delete(entry.path);
          else state.expanded.add(entry.path);
          renderTree();
        };
        item.append(button);
        if (expanded) {
          const children = el("div", "tree-children");
          add(entry.children || [], children);
          if (!entry.children?.length) children.append(el("span", "tree-message", "空文件夹"));
          item.append(children);
        }
      } else {
        button.append(el("span", "tree-indent"), icon(entry.kind === "note" ? "file" : "image"), el("span", "tree-name", entry.kind === "note" ? displayName(entry.name) : entry.name));
        if (entry.kind === "note") button.onclick = () => openNote(entry.path);
        else button.onclick = () => window.open(assetURL(entry.path), "_blank", "noopener,noreferrer");
        item.append(button);
      }
      parent.append(item);
    }
  }
  add(state.vault.tree, root);
  if (!state.vault.tree.length) root.append(el("p", "tree-message", "目录为空"));
}

function renderTabs() {
  const container = $("#tabs");
  container.replaceChildren();
  if (state.graphTab) {
    const tab = el("div", `tab${state.view === "graph" ? " active" : ""}`);
    const select = el("button", "tab-select");
    select.setAttribute("role", "tab");
    select.setAttribute("aria-selected", String(state.view === "graph"));
    select.append(icon("graph"), el("span", "", "关系图谱"));
    select.onclick = () => showGraph(Boolean(graphView.options.focus));
    const close = el("button", "tab-close");
    close.setAttribute("aria-label", "关闭关系图谱");
    close.append(icon("close"));
    close.onclick = () => {
      state.graphTab = false;
      if (state.view === "graph") {
        if (state.current) openNote(state.current);
        else { showReading(); history.pushState(null, "", "/admin/"); showEmpty(); }
      }
      renderTabs();
    };
    tab.append(select, close);
    container.append(tab);
  }
  for (const path of state.tabs) {
    const active = state.view !== "graph" && path === state.current;
    const tab = el("div", `tab${active ? " active" : ""}`);
    const select = el("button", "tab-select");
    select.setAttribute("role", "tab");
    select.setAttribute("aria-selected", String(active));
    select.title = path;
    select.append(icon("file"), el("span", "", displayName(path)));
    if (editor.dirty && path === state.current) {
      const dirty = el("span", "tab-dirty", "•");
      dirty.setAttribute("aria-label", "未保存");
      select.append(dirty);
    }
    select.onclick = () => openNote(path);
    const close = el("button", "tab-close");
    close.setAttribute("aria-label", `关闭 ${displayName(path)}`);
    close.append(icon("close"));
    close.onclick = () => closeTab(path);
    tab.append(select, close);
    container.append(tab);
  }
}

async function closeTab(path) {
  const closingEditor = editor.active && path === state.current;
  if (closingEditor && !await leaveEditor()) return;
  if (closingEditor) path = state.current;
  const index = state.tabs.indexOf(path);
  state.tabs = state.tabs.filter((p) => p !== path);
  if (state.view === "graph") {
    if (path === state.current) {
      state.current = state.tabs[0] || null;
      graphView.show(state.current, Boolean(graphView.options.focus));
      history.replaceState(null, "", graphLocation(Boolean(graphView.options.focus)));
    }
    renderTabs();
    return;
  }
  if (path !== state.current) { renderTabs(); return; }
  const next = state.tabs[Math.min(index, state.tabs.length - 1)];
  if (next) openNote(next);
  else {
    state.request++;
    state.current = null;
    state.note = null;
    history.pushState(null, "", "/admin/");
    showEmpty();
    renderTabs();
    renderTree();
  }
}

function showReading() {
  state.view = "note";
  graphView.hide();
  $("#graph-view").hidden = true;
  $("#editor-view").hidden = true;
  $(".document-layout").hidden = false;
  $(".view-switch").hidden = false;
  $("#outline-toggle").hidden = false;
  $("#local-graph-button").hidden = false;
  $("#edit-button").hidden = false;
  $("#graph-button").classList.remove("selected");
  $("#files-button").classList.add("selected");
  $("#publish-button").disabled = !state.note;
}

function applySnapshot(snapshot) {
  state.vault = snapshot;
  $("#settings-button").hidden = !snapshot.managed;
  $("#end-test-button").hidden = !snapshot.testMode;
  $("#vault-name").textContent = snapshot.name;
  $("#vault-name").title = snapshot.name;
  $("#vault-count").textContent = `${snapshot.notes.length} 篇笔记 · ${snapshot.attachments} 个附件`;
  $("#admin-notes-count").textContent = `${snapshot.notes.length} 篇笔记`;
  $("#admin-assets-count").textContent = `${snapshot.attachments} 个附件`;
  $("#access-mode").textContent = snapshot.writable ? "可编辑" : "只读";
  $("#new-note-button").disabled = !snapshot.writable;
  $("#new-note-button").title = snapshot.writable ? "新建 Markdown 笔记" : "只读模式";
  $("#edit-button").disabled = !snapshot.writable || !state.note;
  $("#edit-button").title = snapshot.writable ? "编辑当前笔记" : "只读模式";
  renderTree();
}

async function leaveEditor() {
  if (!editor.active) return true;
  if (!await editor.mayLeave()) return false;
  editor.close();
  showReading();
  return true;
}

async function exitEditor() {
  if (!await leaveEditor()) return;
  const path = editor.path;
  if (!editor.draft.isNew) { await openNote(path, { history: false }); return; }
  state.tabs = state.tabs.filter((item) => item !== path);
  const next = state.tabs[0] || state.vault?.notes[0]?.path;
  if (next) await openNote(next);
  else {
    state.current = null; state.note = null;
    history.replaceState(null, "", "/admin/");
    showEmpty(); renderTabs(); renderTree();
  }
}

async function startEditor(note, isNew = false) {
  if (state.vault.saveMode === "git") {
    try {
      const git = await api("/admin/api/git");
      renderGit(git);
      if (!git.canWrite) { toast(git.message); return; }
      if (!isNew && note.gitHead !== git.head) { toast("仓库版本已变化，请重新读取笔记再编辑"); return; }
      note = { ...note, gitHead: git.head };
    } catch (error) { toast(error.message); return; }
  }
  state.view = "edit";
  $("#publish-button").disabled = true;
  state.current = note.path;
  state.note = note;
  if (!state.tabs.includes(note.path)) state.tabs.push(note.path);
  graphView.hide();
  $("#graph-view").hidden = true;
  $(".document-layout").hidden = true;
  $("#editor-view").hidden = false;
  $(".view-switch").hidden = true;
  $("#outline-toggle").hidden = true;
  $("#local-graph-button").hidden = true;
  $("#edit-button").hidden = true;
  $("#graph-button").classList.remove("selected");
  $("#files-button").classList.add("selected");
  $("#breadcrumbs").textContent = `${state.vault.name} / ${note.path}`;
  history.replaceState(null, "", noteURL(note.path));
  editor.open(note, isNew);
  reveal(note.path);
  renderTree(); renderTabs();
  if (window.matchMedia("(max-width: 760px)").matches) setPane(false);
}

async function newNote() {
  if (!state.vault?.writable || $("#new-note-dialog").open) return;
  const dialog = $("#new-note-dialog");
  const parent = state.current?.includes("/") ? state.current.slice(0, state.current.lastIndexOf("/") + 1) : "";
  $("#new-note-path").value = `${parent}未命名笔记.md`;
  $("#new-note-path").setCustomValidity("");
  dialog.returnValue = "cancel";
  const closed = new Promise((resolve) => dialog.addEventListener("close", resolve, { once: true }));
  dialog.showModal();
  $("#new-note-path").focus(); $("#new-note-path").select();
  await closed;
  if (dialog.returnValue !== "create" || !await leaveEditor()) return;
  const path = $("#new-note-path").value;
  startEditor({ path, title: displayName(path), source: `# ${displayName(path)}\n\n`, revision: "" }, true);
}

function graphLocation(local) {
  const params = new URLSearchParams({ view: "graph" });
  if (state.current) params.set("focus", state.current);
  if (local && state.current) params.set("local", "1");
  return `/admin/?${params}`;
}

async function showGraph(local = false, recordHistory = true) {
  if (!await leaveEditor()) return;
  state.request++;
  state.view = "graph";
  $("#publish-button").disabled = true;
  state.graphTab = true;
  $("#graph-view").hidden = false;
  $("#editor-view").hidden = true;
  $(".document-layout").hidden = true;
  $(".view-switch").hidden = true;
  $("#outline-toggle").hidden = true;
  $("#local-graph-button").hidden = true;
  $("#edit-button").hidden = true;
  $("#graph-button").classList.add("selected");
  $("#files-button").classList.remove("selected");
  $("#breadcrumbs").textContent = `${state.vault?.name || "知识库"} / 关系图谱`;
  $("#note-status").textContent = "图谱 · 本地链接索引";
  document.title = "关系图谱 · OB Vault";
  if (recordHistory) history.pushState(null, "", graphLocation(local));
  renderTabs();
  if (window.matchMedia("(max-width: 760px)").matches) setPane(false);
  await graphView.show(state.current, local);
}

function showEmpty(message = "选择一篇笔记", error = false) {
  $("#publish-button").disabled = true;
  const empty = el("div", "empty-state");
  empty.append(el("h1", "", error ? "无法打开笔记" : "笔记"), el("p", "", message));
  if (error) {
    const retry = el("button", "primary-button", "刷新目录");
    retry.onclick = () => loadVault(false);
    empty.append(retry);
  }
  $("#document-container").replaceChildren(empty);
  $("#outline").replaceChildren(el("p", "outline-empty", "暂无大纲"));
  $("#note-info").replaceChildren();
  $("#breadcrumbs").textContent = state.vault?.name || "工作区";
  $("#note-status").textContent = "Markdown · UTF-8";
  document.title = "OB Vault · 知识库";
}

function renderDocument() {
  const note = state.note;
  if (!note) return;
  $("#publish-button").disabled = false;
  const container = $("#document-container");
  container.replaceChildren();
  if (note.properties && !state.source) {
    const properties = el("details", "properties");
    properties.append(el("summary", "", "笔记属性"), el("pre", "", note.properties));
    container.append(properties);
  }
  if (state.source) {
    const source = el("pre", "source-view");
    source.append(el("code", "", note.source));
    container.append(source);
  } else {
    const article = el("article", "markdown-body");
    // HTML comes exclusively from the server's safe-mode Markdown renderer.
    article.innerHTML = note.html;
    if (!article.querySelector("h1")) article.prepend(el("h1", "", note.title));
    article.querySelectorAll('a[href]').forEach((link) => {
      const url = new URL(link.getAttribute("href"), location.href);
      if (url.origin !== location.origin || url.pathname === "/admin/api/asset") {
        link.target = "_blank";
        link.rel = "noopener noreferrer";
      }
    });
    article.querySelectorAll("pre").forEach((pre) => {
      const code = pre.querySelector("code");
      const language = [...(code?.classList || [])].find((c) => c.startsWith("language-"));
      const label = el("div", "code-language", language ? language.slice(9) : "TEXT");
      pre.before(label);
    });
    container.append(article);
  }
  $("#breadcrumbs").replaceChildren();
  const parts = [state.vault?.name || "知识库", ...note.path.split("/")];
  parts.forEach((part, i) => {
    if (i) $("#breadcrumbs").append(icon("chevron"));
    $("#breadcrumbs").append(el("span", i === parts.length - 1 ? "current" : "", part));
  });
  $("#preview-button").classList.toggle("active", !state.source);
  $("#source-button").classList.toggle("active", state.source);
  $("#preview-button").setAttribute("aria-pressed", String(!state.source));
  $("#source-button").setAttribute("aria-pressed", String(state.source));
  const outline = $("#outline");
  outline.replaceChildren();
  for (const heading of note.headings) {
    const link = el("a", `outline-link level-${heading.level}`, heading.text);
    link.href = noteURL(note.path, `#${encodeURIComponent(heading.id)}`);
    link.onclick = (event) => {
      event.preventDefault();
      if (state.source) { state.source = false; renderDocument(); }
      history.replaceState(null, "", noteURL(note.path, `#${encodeURIComponent(heading.id)}`));
      scrollToFragment(location.hash);
      outline.querySelectorAll(".active").forEach((a) => a.classList.remove("active"));
      link.classList.add("active");
    };
    outline.append(link);
  }
  if (!note.headings.length) outline.append(el("p", "outline-empty", "暂无标题"));
  const info = $("#note-info");
  info.replaceChildren();
  for (const [key, value] of [["字数（约）", note.words.toLocaleString()], ["阅读时间", `${Math.max(1, Math.ceil(note.words / 350))} 分钟`], ["文件修改", new Date(note.modified).toLocaleDateString("zh-CN")]]) {
    const row = el("div", "info-row");
    row.append(el("span", "", key), el("span", "", value));
    info.append(row);
  }
  $("#note-status").textContent = `${note.words.toLocaleString()} 字（约） · Markdown · UTF-8`;
  document.title = `${note.title} · OB Vault`;
  $("#edit-button").disabled = !state.vault?.writable;
}

function scrollToFragment(fragment) {
  if (!fragment) { $("#document-scroll").scrollTop = 0; return; }
  let id;
  try { id = decodeURIComponent(fragment.slice(1)); } catch { return; }
  const target = [...$("#document-container").querySelectorAll("[id]")].find((n) => n.id === id);
  if (target) target.scrollIntoView({ block: "start", behavior: "smooth" });
  else toast(id.startsWith("^") ? "不支持块引用定位" : "未找到对应章节");
}

async function openNote(path, options = {}) {
  if (editor.active && editor.path === path) return;
  if (!await leaveEditor()) return;
  showReading();
  const request = ++state.request;
  state.current = path;
  state.note = null;
  $("#publish-button").disabled = true;
  $("#edit-button").disabled = true;
  if (!state.tabs.includes(path)) state.tabs.push(path);
  reveal(path);
  renderTree();
  renderTabs();
  const fragment = options.fragment || "";
  if (options.history !== false) history.pushState(null, "", noteURL(path, fragment));
  $("#document-container").replaceChildren(el("div", "loading-state", "正在打开笔记…"));
  $("#outline").replaceChildren();
  $("#note-info").replaceChildren();
  $("#breadcrumbs").textContent = path;
  $("#note-status").textContent = "正在读取…";
  if (window.matchMedia("(max-width: 760px)").matches) setPane(false);
  try {
    const note = await api(`/admin/api/note?${new URLSearchParams({ path })}`);
    if (request !== state.request) return;
    state.note = note;
    renderDocument();
    requestAnimationFrame(() => scrollToFragment(fragment));
  } catch (error) {
    if (request !== state.request) return;
    showEmpty(error.message, true);
  }
}

async function loadVault(initial = false) {
  if (!initial && !await leaveEditor()) return;
  const button = $("#refresh-button");
  button.disabled = true;
  button.classList.add("refreshing");
  try {
    const snapshot = await api("/admin/api/vault");
    applySnapshot(snapshot);
    if (initial) {
      const params = new URLSearchParams(location.search);
      const requested = params.get("note");
      const welcome = snapshot.notes.find((n) => n.name === "欢迎.md") || snapshot.notes[0];
      if (params.get("view") === "graph") {
        state.current = params.get("focus") || welcome?.path || null;
        await showGraph(params.get("local") === "1", false);
        return;
      }
      if (requested || welcome) {
        const path = requested || welcome.path;
        if (!requested) history.replaceState(null, "", noteURL(path));
        await openNote(path, { history: false, fragment: location.hash });
      } else showEmpty("暂无笔记");
    } else {
      if (state.view === "graph") await graphView.load(true);
      else {
        graphView.invalidate();
        if (state.current) await openNote(state.current, { history: false, fragment: location.hash });
        else showEmpty();
      }
      toast("目录已刷新");
    }
  } catch (error) {
    if (!state.vault) showEmpty(error.message, true);
    else toast(error.message);
  } finally {
    button.disabled = false;
    button.classList.remove("refreshing");
  }
}

$("#document-container").addEventListener("click", (event) => {
  const link = event.target.closest("a[href]");
  if (!link || event.ctrlKey || event.metaKey || event.shiftKey || event.altKey || event.button !== 0) return;
  const url = new URL(link.getAttribute("href"), location.href);
  if (url.origin !== location.origin) return;
  if (url.hash === "#unresolved-link") { event.preventDefault(); toast("链接不存在或有重名，请检查路径"); return; }
  if (url.pathname === "/admin/" && url.searchParams.has("note")) {
    event.preventDefault();
    if (url.searchParams.get("note") === state.current) {
      history.pushState(null, "", url);
      scrollToFragment(url.hash);
    } else openNote(url.searchParams.get("note"), { fragment: url.hash });
  }
});

$("#file-search").addEventListener("input", renderTree);
$("#file-search").addEventListener("keydown", (event) => {
  if (event.key === "Escape") { event.target.value = ""; renderTree(); event.target.blur(); }
  if (event.key === "Enter") $("#file-tree .search-result")?.click();
});
$("#search-button").onclick = () => { setPane(true); $("#file-search").focus(); $("#file-search").select(); };
$("#files-button").onclick = () => { setPane(true); $("#file-search").value = ""; renderTree(); };
$("#edit-button").onclick = () => { if (state.vault?.writable && state.note) startEditor(state.note); };
$("#new-note-button").onclick = newNote;
$("#settings-button").onclick = async () => { if (!editor.busy && !gitBusy && await leaveEditor()) location.assign("/admin/settings"); };
$("#end-test-button").onclick = async () => {
  if (editor.busy || gitBusy) return;
  const button = $("#end-test-button");
  button.disabled = true;
  try {
    await api("/admin/api/test/stop", { method: "POST" });
    editor.close();
    graphView.hide();
    location.replace("about:blank");
  } catch (error) { button.disabled = false; toast(error.message); }
};
$("#new-note-path").oninput = () => $("#new-note-path").setCustomValidity("");
$("#new-note-form").onsubmit = (event) => {
  if (event.submitter?.value !== "create") return;
  const input = $("#new-note-path");
  let path = input.value.trim();
  if (!/\.md$/i.test(path)) path += ".md";
  let error = "";
  if (path.split("/").some((part) => !part || part.startsWith(".")) || path.includes("\\")) error = "请使用非隐藏的仓库相对路径，例如：笔记/新笔记.md";
  else if (state.vault.notes.some((note) => note.path === path)) error = "同名笔记已存在，请使用其他文件名";
  if (error) { event.preventDefault(); input.setCustomValidity(error); input.reportValidity(); }
  else input.value = path;
};
$(".brand").onclick = async (event) => {
  event.preventDefault();
  if (!await leaveEditor()) return;
  const first = state.vault?.notes[0];
  if (first) openNote(first.path);
  else { history.pushState(null, "", "/admin/"); showEmpty(); }
};
$("#graph-button").onclick = () => showGraph();
$("#local-graph-button").onclick = () => {
  if (state.current) showGraph(true);
  else toast("请先打开一篇笔记");
};
$("#pane-toggle").onclick = () => setPane(window.matchMedia("(max-width: 760px)").matches ? !document.body.classList.contains("pane-open") : document.body.classList.contains("pane-hidden"));
$("#close-pane").onclick = $("#pane-backdrop").onclick = () => setPane(false);
$("#collapse-button").onclick = () => { state.expanded.clear(); renderTree(); };
$("#refresh-button").onclick = () => loadVault();
$("#back-button").onclick = () => history.back();
$("#forward-button").onclick = () => history.forward();
$("#outline-toggle").onclick = () => {
  const hidden = document.body.classList.toggle("outline-hidden");
  $("#outline-toggle").setAttribute("aria-expanded", String(!hidden));
};
$("#preview-button").onclick = () => { state.source = false; renderDocument(); };
$("#source-button").onclick = () => { state.source = true; renderDocument(); };
$("#theme-button").onclick = () => {
  if (!state.vault) return;
  const theme = document.documentElement.dataset.theme === "dark" ? "light" : "dark";
  document.documentElement.dataset.theme = theme;
  try { if (!state.vault?.testMode) localStorage.setItem("ob-vault-theme", theme); } catch { /* Storage may be disabled. */ }
};
window.addEventListener("popstate", async () => {
  if (editor.active && !await leaveEditor()) { history.pushState(null, "", editor.location); return; }
  const params = new URLSearchParams(location.search);
  if (params.get("view") === "graph") {
    state.current = params.get("focus") || state.current;
    showGraph(params.get("local") === "1", false);
    return;
  }
  showReading();
  const path = params.get("note");
  if (path) openNote(path, { history: false, fragment: location.hash });
  else { state.request++; state.current = null; state.note = null; showEmpty(); renderTree(); renderTabs(); }
});
document.addEventListener("keydown", (event) => {
  if (editor.active && !event.defaultPrevented && (event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") {
    event.preventDefault(); editor.save(); return;
  }
  if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
    event.preventDefault(); $("#search-button").click();
  }
  if (event.key === "Escape" && window.matchMedia("(max-width: 760px)").matches) setPane(false);
});
try {
  const theme = localStorage.getItem("ob-vault-theme");
  if (theme === "light" || theme === "dark") document.documentElement.dataset.theme = theme;
} catch { /* Keep the default theme when local storage is unavailable. */ }
$(".search-field kbd").textContent = /Mac|iPhone|iPad/.test(navigator.platform) ? "⌘ K" : "Ctrl K";
loadVault(true);

let gitBusy = false;
let gitStatus = null;
const gitLabels = { disabled: "本地目录", current: "已同步", dirty: "本地修改待处理", offline: "远端不可用", ahead: "本地提交待推送", changed: "远端已变化", blocked: "仓库状态异常" };
function renderGit(status) {
  gitStatus = status;
  $("#git-state").textContent = gitLabels[status.state] || status.state;
  $("#git-message").textContent = status.state === "current" ? "" : status.message;
  $("#git-branch").textContent = status.branch || "—";
  $("#git-head").textContent = status.head?.slice(0, 12) || "—";
  $("#git-remote").textContent = status.remoteHead?.slice(0, 12) || "—";
  $("#git-check").disabled = gitBusy;
  $("#git-pull").disabled = gitBusy || status.state !== "changed";
  $("#git-push").disabled = gitBusy || !status.canPush;
  $("#git-manage-button").textContent = `Git 管理 · ${gitLabels[status.state] || status.state}`;
}
async function gitOperation(action = "check") {
  if (gitBusy || editor.busy) return;
  // Pull/push must never change the checkout under an active editor.
  if (action !== "check" && !await leaveEditor()) return;
  gitBusy = true;
  for (const id of ["git-check", "git-pull", "git-push"]) $(`#${id}`).disabled = true;
  $("#git-message").textContent = action === "check" ? "正在核对远端…" : "同步中…";
  let errorMessage;
  try {
    const result = await api(`/admin/api/git${action === "check" ? "" : `/${action}`}`, { method: action === "check" ? "GET" : "POST" });
    renderGit(result);
    if (action !== "check") {
      await loadVault();
      toast(result.warning || result.message);
    }
  } catch (error) {
    errorMessage = error.message;
    try { renderGit(await api("/admin/api/git")); } catch { /* Keep the action error visible. */ }
  } finally {
    gitBusy = false;
    if (gitStatus) renderGit(gitStatus);
    else $("#git-check").disabled = false;
    if (errorMessage) $("#git-message").textContent = errorMessage;
  }
}
function showAdmin() {
  if (!$("#admin-dialog").open) $("#admin-dialog").showModal();
}
$("#admin-button").onclick = $("#git-manage-button").onclick = showAdmin;
$("#admin-close").onclick = () => $("#admin-dialog").close();
$("#git-check").onclick = () => gitOperation();
$("#git-pull").onclick = async () => {
  // Close the modal before any dirty-draft confirmation is opened.
  $("#admin-dialog").close(); await gitOperation("pull"); showAdmin();
};
$("#git-push").onclick = async () => { $("#admin-dialog").close(); await gitOperation("push"); showAdmin(); };
$("#logout-button").onclick = async () => {
  if (gitBusy || editor.busy) return;
  $("#admin-dialog").close();
  if (!await leaveEditor()) return;
  try { await api("/admin/api/logout", { method: "POST" }); location.assign("/admin/login"); }
  catch (error) { toast(error.message); }
};

let publicationNote = null;
let publicationRecord = null;
let publicationBusy = false;
function localDateInput(value) {
  const date = new Date(value);
  return new Date(date.getTime() - date.getTimezoneOffset() * 60000).toISOString().slice(0, 19);
}
function renderPublication() {
  const record = publicationRecord;
  $("#publication-enabled").checked = Boolean(record?.enabled);
  $("#publication-enabled").disabled = publicationBusy;
  $("#publication-date").disabled = publicationBusy || !record?.id;
  $("#publication-date-save").disabled = publicationBusy || !record?.id;
  $("#publication-refresh").disabled = publicationBusy;
  $("#publication-close").disabled = publicationBusy;
  $("#publication-date").value = record?.publishedAt ? localDateInput(record.publishedAt) : "";
  $("#publication-link").hidden = !record?.enabled;
  if (record?.url) $("#publication-link").href = record.url;
}
$("#publish-button").onclick = async () => {
  if (!state.note || state.view !== "note") return;
  const note = state.note;
  $("#publish-button").disabled = true;
  try {
    const record = await api(`/admin/api/publication?${new URLSearchParams({ path: note.path })}`);
    if (state.note !== note || state.view !== "note") return;
    publicationNote = note;
    publicationRecord = record;
    $("#publication-note-title").textContent = note.title;
    $("#publication-status").textContent = "";
    renderPublication();
    $("#publication-dialog").showModal();
  } catch (error) { toast(error.message); }
  finally { $("#publish-button").disabled = !state.note || state.view !== "note"; }
};
$("#publication-close").onclick = () => $("#publication-dialog").close();
$("#publication-dialog").addEventListener("cancel", (event) => { if (publicationBusy) event.preventDefault(); });
async function updatePublication(enabled, date) {
  if (publicationBusy) return;
  publicationBusy = true;
  renderPublication();
  $("#publication-status").textContent = "保存中…";
  try {
    publicationRecord = await api("/admin/api/publication", { method: "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ path: publicationNote.path, enabled, publishedAt: date, revision: publicationNote.revision, gitHead: publicationNote.gitHead }) });
    $("#publication-status").textContent = date ? "发布时间已保存" : enabled ? "已发布" : "已取消发布";
  } catch (error) { $("#publication-status").textContent = error.message; }
  finally { publicationBusy = false; renderPublication(); }
}
$("#publication-enabled").onchange = (event) => updatePublication(event.target.checked);
$("#publication-date-form").onsubmit = (event) => {
  event.preventDefault();
  const date = new Date($("#publication-date").value);
  if (!Number.isFinite(date.getTime()) || date.getTime() > Date.now() + 60000) { $("#publication-status").textContent = "发布时间不能晚于当前时间"; return; }
  updatePublication(publicationRecord.enabled, date.toISOString());
};
$("#publication-refresh").onclick = async () => {
  if (publicationBusy) return;
  publicationBusy = true; renderPublication();
  $("#publication-status").textContent = "更新中…";
  try {
    await api("/admin/api/publications/refresh", { method: "POST" });
    publicationRecord = await api(`/admin/api/publication?${new URLSearchParams({ path: publicationNote.path })}`);
    $("#publication-status").textContent = "公开副本已更新";
  } catch (error) { $("#publication-status").textContent = error.message; }
  finally { publicationBusy = false; renderPublication(); }
};
