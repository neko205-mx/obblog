import { Draft, wikiSuggestions, indentSelection, imageFiles, pastedImageName, imageEmbed } from "./editor-model.mjs";

const $ = (id) => document.getElementById(id);

async function jsonRequest(url, options) {
  const response = await fetch(url, { cache: "no-store", ...options });
  const data = await response.json();
  if (!response.ok) {
    const error = new Error(data.error || `请求失败（${response.status}）`);
    error.status = response.status;
    throw error;
  }
  return data;
}

export class NoteEditor {
  constructor({ onChange, onSaved, onUploaded, onClose, notify, notes, gitMode }) {
    Object.assign(this, { onChange, onSaved, onUploaded, onClose, notify, notes, gitMode });
    this.active = false;
    this.saving = false;
    this.uploading = false;
    this.previewID = 0;
    this.textarea = $("note-editor");
    this.textarea.addEventListener("input", () => this.input());
    this.textarea.addEventListener("scroll", () => { $("editor-lines").scrollTop = this.textarea.scrollTop; });
    this.textarea.addEventListener("keydown", (event) => this.keydown(event));
    this.textarea.addEventListener("click", () => this.suggestions());
    this.textarea.addEventListener("compositionstart", () => { this.composing = true; this.hideSuggestions(); });
    this.textarea.addEventListener("compositionend", () => { this.composing = false; this.input(); });
    this.textarea.addEventListener("paste", event => {
      const files = imageFiles(event.clipboardData);
      if (!files.length) return;
      event.preventDefault();
      this.uploadImages(files, true);
    });
    this.textarea.addEventListener("dragover", event => {
      if (!Array.from(event.dataTransfer?.types || []).includes("Files")) return;
      event.preventDefault();
      event.dataTransfer.dropEffect = this.busy || !navigator.onLine ? "none" : "copy";
      this.textarea.classList.add("image-drop-target");
    });
    this.textarea.addEventListener("dragleave", () => this.textarea.classList.remove("image-drop-target"));
    this.textarea.addEventListener("drop", event => {
      this.textarea.classList.remove("image-drop-target");
      if (!Array.from(event.dataTransfer?.types || []).includes("Files")) return;
      event.preventDefault();
      const files = imageFiles(event.dataTransfer);
      if (files.length) this.uploadImages(files);
      else this.notify("请选择图片文件");
    });
    $("insert-image").onclick = () => $("image-files").click();
    $("image-files").onchange = event => {
      this.uploadImages(Array.from(event.target.files));
      event.target.value = "";
    };
    $("save-note").onclick = () => this.save();
    $("close-editor").onclick = () => this.onClose();
    $("editor-preview-toggle").onclick = () => {
      const preview = $("editor-view").classList.toggle("preview-only");
      $("editor-preview-toggle").setAttribute("aria-pressed", String(preview));
      $("editor-preview-toggle").textContent = preview ? "返回编辑" : "展开预览";
    };
    $("editor-path").addEventListener("input", () => this.schedulePreview());
    for (const button of document.querySelectorAll("[data-format]")) button.onclick = () => this.format(button.dataset.format);
    $("show-server-note").onclick = () => this.showServer();
    $("copy-draft").onclick = async () => {
      try { await navigator.clipboard.writeText(this.draft.source); this.notify("草稿已复制"); }
      catch { this.textarea.focus(); this.textarea.select(); this.notify("复制失败，请手动复制已选中的草稿"); }
    };
    $("editor-preview").addEventListener("click", (event) => {
      if (event.target.closest("a")) { event.preventDefault(); this.notify("退出编辑后可打开链接"); }
    });
    window.addEventListener("beforeunload", (event) => {
      if (this.active && (this.draft.dirty || this.busy)) { event.preventDefault(); event.returnValue = ""; }
    });
    window.addEventListener("online", () => this.status());
    window.addEventListener("offline", () => this.status());
  }

  get path() { return this.draft?.path; }
  get dirty() { return this.active && this.draft.dirty; }
  get busy() { return this.saving || this.uploading; }

  open(note, isNew = false) {
    this.close();
    this.active = true;
    this.draft = new Draft({ ...note, isNew });
    this.gitHead = note.gitHead || "";
    this.warning = "";
    this.synced = false;
    this.location = location.href;
    this.textarea.value = this.draft.text;
    $("editor-path").value = note.path;
    $("editor-path").readOnly = !isNew;
    $("editor-path").title = isNew ? "可修改新笔记路径，目标目录必须已经存在" : "不支持重命名";
    $("editor-conflict").hidden = true;
    $("server-version").hidden = true;
    $("server-version").textContent = "";
    $("editor-view").classList.remove("preview-only");
    $("editor-preview-toggle").textContent = "展开预览";
    $("editor-preview-toggle").setAttribute("aria-pressed", "false");
    this.updateLines();
    this.status();
    this.schedulePreview(0);
    this.textarea.focus();
  }

  close() {
    this.active = false;
    clearTimeout(this.previewTimer);
    this.previewController?.abort();
    this.previewID++;
    this.hideSuggestions();
  }

  status(message, kind = "") {
    if (!this.active) return;
    const offline = !navigator.onLine;
    this.textarea.readOnly = offline || this.busy;
    $("editor-path").disabled = offline || this.busy;
    $("save-note").disabled = offline || this.busy || !this.draft.dirty;
    $("insert-image").disabled = offline || this.busy;
    $("save-note").textContent = this.saving ? "保存中…" : this.gitMode?.() ? "保存并推送" : "保存到本地";
    for (const button of document.querySelectorAll("[data-format]")) button.disabled = offline || this.busy;
    const label = this.uploading ? "正在上传图片…" : offline ? "离线，编辑已暂停" : message || this.warning || (this.draft.dirty ? "未保存" : this.synced ? "已保存并推送" : "已载入");
    $("editor-status").textContent = label;
    $("editor-status").dataset.state = offline ? "error" : kind;
    $("editor-character-count").textContent = `${this.draft.text.split("\n").length} 行 · ${this.draft.text.length.toLocaleString()} 字符`;
    this.onChange?.();
  }

  updateLines() {
    const count = this.textarea.value.split("\n").length;
    $("editor-lines").textContent = Array.from({ length: count }, (_, i) => i + 1).join("\n");
    $("editor-lines").scrollTop = this.textarea.scrollTop;
  }

  input() {
    if (!this.active) return;
    this.draft.text = this.textarea.value;
    this.updateLines();
    this.status();
    this.schedulePreview();
    if (!this.composing) this.suggestions();
  }

  schedulePreview(delay = 350) {
    clearTimeout(this.previewTimer);
    this.previewController?.abort();
    const id = ++this.previewID;
    this.previewTimer = setTimeout(() => this.preview(id), delay);
  }

  async preview(id) {
    if (!this.active || id !== this.previewID) return;
    this.previewController = new AbortController();
    $("editor-preview-state").textContent = "更新预览…";
    try {
      const result = await jsonRequest("/admin/api/preview", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ path: $("editor-path").value, source: this.draft.source }), signal: this.previewController.signal,
      });
      if (!this.active || id !== this.previewID) return;
      // Preview uses the same safe Markdown renderer as the normal reading view.
      $("editor-preview").innerHTML = result.html;
      if (result.properties) {
        const properties = document.createElement("pre");
        properties.className = "editor-property-preview";
        properties.textContent = result.properties;
        $("editor-preview").prepend(properties);
      }
      $("editor-preview-state").textContent = "实时预览";
    } catch (error) {
      if (error.name === "AbortError" || id !== this.previewID) return;
      $("editor-preview-state").textContent = "预览加载失败";
    }
  }

  async save() {
    if (!this.active || this.busy || !navigator.onLine) return false;
    if (!this.draft.dirty) return true;
    const submitted = this.draft.submission($("editor-path").value.trim());
    if (this.gitMode?.()) submitted.gitHead = this.gitHead;
    if (new TextEncoder().encode(submitted.source).length > 8 * 1024 * 1024) { this.status("超过 8 MiB，未保存", "error"); return false; }
    this.saving = true;
    this.hideSuggestions();
    this.status();
    try {
      const saved = await jsonRequest("/admin/api/note", {
        method: this.draft.isNew ? "POST" : "PUT", headers: { "Content-Type": "application/json" }, body: JSON.stringify(submitted),
      });
      const previousPath = this.draft.path;
      this.draft.accept(saved, submitted);
      this.gitHead = saved.gitHead || "";
      this.warning = saved.warning || "";
      this.synced = saved.synced === true;
      $("editor-path").value = saved.path;
      $("editor-path").readOnly = true;
      $("editor-path").title = "不支持重命名";
      $("editor-conflict").hidden = true;
      $("server-version").hidden = true;
      try { await this.onSaved?.(saved, submitted.source, previousPath); }
      catch { this.notify("笔记已保存，但目录刷新失败，请稍后手动刷新"); }
      this.status(saved.warning || (saved.synced ? "已保存并推送" : `已保存到本地 ${new Date(saved.modified).toLocaleTimeString("zh-CN")}`), saved.warning ? "error" : "");
      if (saved.warning) this.notify(saved.warning);
      return !saved.warning;
    } catch (error) {
      if (error.status === 409) {
        $("editor-conflict").hidden = false;
        $("show-server-note").disabled = this.draft.isNew;
      }
      this.status(error.message || "保存失败", "error");
      return false;
    } finally {
      this.saving = false;
      this.status($("editor-status").textContent, $("editor-status").dataset.state);
    }
  }

  async uploadImages(files, pasted = false) {
    if (!this.active || this.busy || !files.length) return;
    if (!navigator.onLine) { this.notify("离线，无法上传图片"); return; }
    if (files.some(file => file.size > 20 * 1024 * 1024)) { this.notify("单张图片不能超过 20 MiB"); return; }
    // Freeze editing during upload so the insertion range cannot drift while
    // the server creates files and (in Git mode) advances the checkout HEAD.
    let start = this.textarea.selectionStart, end = this.textarea.selectionEnd;
    this.uploading = true;
    this.hideSuggestions();
    this.status();
    let errorMessage = "";
    try {
      for (const file of files) {
        const body = new FormData();
        body.append("file", file, pasted ? pastedImageName(file) : file.name);
        if (this.gitMode?.()) body.append("gitHead", this.gitHead);
        const saved = await jsonRequest("/admin/api/asset", { method: "POST", body });
        this.gitHead = saved.gitHead || this.gitHead;
        this.warning = saved.warning || "";
        this.synced = false; // The note's new embed still needs saving.
        const insert = imageEmbed(saved.path) + (files.length > 1 ? "\n" : "");
        this.textarea.setRangeText(insert, start, end, "end");
        start += insert.length;
        end = start;
        this.input();
        try { await this.onUploaded?.(saved); }
        catch { this.notify("图片已上传，目录刷新失败，请手动刷新"); }
        if (saved.warning) { this.notify(saved.warning); break; }
      }
    } catch (error) {
      errorMessage = error.message || "图片上传失败";
      this.notify(errorMessage);
    } finally {
      this.uploading = false;
      this.textarea.focus();
      this.textarea.setSelectionRange(start, end);
      this.status(errorMessage || this.warning, errorMessage || this.warning ? "error" : "");
      this.schedulePreview(0);
    }
  }

  async showServer() {
    try {
      const note = await jsonRequest(`/admin/api/note?${new URLSearchParams({ path: this.draft.path })}`);
      $("server-version").textContent = note.source;
      $("server-version").hidden = false;
    } catch (error) { this.notify(error.message); }
  }

  async mayLeave() {
    if (!this.active) return true;
    if (this.busy) { this.notify(this.uploading ? "正在上传图片，请稍后再切换" : "正在保存，请稍后再切换"); return false; }
    if (!this.draft.dirty) return true;
    const result = await this.askUnsaved();
    if (result === "discard") return true;
    if (result === "save") return await this.save();
    return false;
  }

  askUnsaved() {
    if (this.guardPromise) return this.guardPromise;
    const dialog = $("unsaved-dialog");
    this.guardPromise = new Promise((resolve) => {
      const finish = () => {
        dialog.removeEventListener("close", finish);
        this.guardPromise = null;
        resolve(dialog.returnValue);
      };
      dialog.addEventListener("close", finish);
      dialog.returnValue = "cancel";
      dialog.showModal();
    });
    return this.guardPromise;
  }

  hideSuggestions() {
    this.completion = null;
    $("wiki-completions").hidden = true;
    this.textarea.removeAttribute("aria-activedescendant");
  }

  suggestions() {
    if (this.composing || this.textarea.selectionStart !== this.textarea.selectionEnd) { this.hideSuggestions(); return; }
    this.completion = wikiSuggestions(this.textarea.value, this.textarea.selectionStart, this.notes());
    if (!this.completion) { this.hideSuggestions(); return; }
    this.completion.index = 0;
    const list = $("wiki-completions");
    list.replaceChildren();
    for (const [index, item] of this.completion.items.entries()) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = item.path;
      button.setAttribute("role", "option");
      button.id = `wiki-option-${index}`;
      button.onmousedown = (event) => event.preventDefault();
      button.onclick = () => this.complete(index);
      list.append(button);
    }
    list.hidden = false;
    this.highlightSuggestion();
  }

  highlightSuggestion() {
    for (const [index, item] of [...$("wiki-completions").children].entries()) item.setAttribute("aria-selected", String(index === this.completion.index));
    this.textarea.setAttribute("aria-activedescendant", `wiki-option-${this.completion.index}`);
  }

  complete(index) {
    const completion = this.completion;
    if (!completion) return;
    const cursor = this.textarea.selectionStart;
    const close = this.textarea.value.slice(cursor, cursor + 2) === "]]" ? cursor + 2 : cursor;
    this.textarea.setRangeText(`[[${completion.items[index].insert}]]`, completion.start, close, "end");
    this.textarea.focus();
    this.input();
    this.hideSuggestions();
  }

  format(kind) {
    if (this.textarea.readOnly) return;
    const start = this.textarea.selectionStart, end = this.textarea.selectionEnd;
    const selection = this.textarea.value.slice(start, end);
    const text = kind === "bold" ? `**${selection || "加粗文字"}**` : kind === "wiki" ? `[[${selection}]]` : `- ${selection || "列表项"}`;
    this.textarea.setRangeText(text, start, end, "end");
    this.textarea.focus();
    if (kind === "wiki") this.textarea.setSelectionRange(start + 2, start + 2 + selection.length);
    this.input();
  }

  keydown(event) {
    if (event.isComposing || this.composing) return;
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "s") { event.preventDefault(); this.save(); return; }
    if (this.completion && ["ArrowDown", "ArrowUp", "Enter", "Tab", "Escape"].includes(event.key)) {
      event.preventDefault();
      if (event.key === "Escape") this.hideSuggestions();
      else if (event.key === "Enter" || event.key === "Tab") this.complete(this.completion.index);
      else {
        this.completion.index = (this.completion.index + (event.key === "ArrowDown" ? 1 : -1) + this.completion.items.length) % this.completion.items.length;
        this.highlightSuggestion();
      }
      return;
    }
    if (this.textarea.readOnly) return;
    if (event.key === "Tab") {
      event.preventDefault();
      const result = indentSelection(this.textarea.value, this.textarea.selectionStart, this.textarea.selectionEnd, event.shiftKey);
      this.textarea.setRangeText(result.insert, result.from, result.to, "preserve");
      this.textarea.setSelectionRange(result.start, result.end);
      this.input();
    }
    if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "b") { event.preventDefault(); this.format("bold"); }
  }
}
