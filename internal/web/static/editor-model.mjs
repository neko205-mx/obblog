export function normalizeForEditor(source) {
  return source.replace(/\r\n|\r/g, "\n");
}

export function encodeDraft(text, original) {
  if (text === normalizeForEditor(original)) return original;
  if (original.includes("\r\n") && !original.replace(/\r\n/g, "").includes("\n")) return text.replace(/\n/g, "\r\n");
  if (original.includes("\r") && !original.includes("\n")) return text.replace(/\n/g, "\r");
  return text;
}

export class Draft {
  constructor({ path, source = "", revision = "", isNew = false }) {
    this.path = path;
    this.original = source;
    this.text = normalizeForEditor(source);
    this.revision = revision;
    this.isNew = isNew;
  }
  get dirty() { return this.isNew || this.text !== normalizeForEditor(this.original); }
  get source() { return encodeDraft(this.text, this.original); }
  submission(path = this.path) { return { path, source: this.source, revision: this.revision }; }
  accept(saved, submitted) {
    this.original = submitted.source;
    this.revision = saved.revision;
    this.path = saved.path;
    this.isNew = false;
  }
}

export function wikiSuggestions(text, cursor, notes) {
  const before = text.slice(0, cursor);
  const start = before.lastIndexOf("[[");
  if (start < 0 || start < before.lastIndexOf("\n")) return null;
  const query = before.slice(start + 2);
  if (/[\]\[|#]/.test(query) || query.length > 150) return null;
  const needle = query.toLocaleLowerCase();
  const items = notes.filter((note) => note.path.toLocaleLowerCase().includes(needle)).slice(0, 8).map((note) => ({ path: note.path, insert: note.path.replace(/\.md$/i, "") }));
  return items.length ? { start, items } : null;
}

export function indentSelection(text, start, end, outdent = false) {
  if (start === end && !outdent) return { from: start, to: end, insert: "    ", start: start + 4, end: end + 4 };
  const from = text.lastIndexOf("\n", start - 1) + 1;
  const to = end > start && text[end - 1] === "\n" ? end - 1 : (() => { const next = text.indexOf("\n", end); return next < 0 ? text.length : next; })();
  const lines = text.slice(from, to).split("\n");
  let firstDelta = 0, totalDelta = 0;
  const insert = lines.map((line, index) => {
    const remove = outdent ? (line.match(/^(?:\t| {1,4})/)?.[0].length || 0) : 0;
    const delta = outdent ? -remove : 4;
    if (index === 0) firstDelta = delta;
    totalDelta += delta;
    return outdent ? line.slice(remove) : "    " + line;
  }).join("\n");
  return { from, to, insert, start: Math.max(from, start + firstDelta), end: Math.max(from, end + totalDelta) };
}
export function imageFiles(transfer) {
  if (!transfer) return [];
  const files = transfer.items?.length
    ? Array.from(transfer.items).filter(item => item.kind === "file").map(item => item.getAsFile()).filter(Boolean)
    : Array.from(transfer.files || []);
  return files.filter(file => file.type.startsWith("image/") || /\.(png|jpe?g|gif|webp|avif|svg)$/i.test(file.name));
}

export function pastedImageName(file, now = new Date()) {
  const stamp = [now.getFullYear(), now.getMonth() + 1, now.getDate(), now.getHours(), now.getMinutes(), now.getSeconds()]
    .map((value, index) => String(value).padStart(index ? 2 : 4, "0")).join("");
  return `Pasted image ${stamp}${file.name.match(/\.[a-z0-9]+$/i)?.[0] || ".png"}`;
}

export function imageEmbed(path) { return `![[${path}]]`; }
