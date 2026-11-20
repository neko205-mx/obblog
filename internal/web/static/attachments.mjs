const assetPath = "/admin/api/asset";

export function attachmentURL(path) {
  return `${assetPath}?${new URLSearchParams({ path })}`;
}

export function attachmentKind(path) {
  const ext = /\.([a-z0-9]+)$/i.exec(path)?.[1].toLowerCase();
  if (["png", "jpg", "jpeg", "gif", "webp", "avif", "svg"].includes(ext)) return "image";
  if (ext === "pdf") return "pdf";
  if (["mp3", "m4a", "ogg", "wav", "flac"].includes(ext)) return "audio";
  if (["mp4", "webm", "mov"].includes(ext)) return "video";
  return "file";
}

function parseURL(value, base) { try { return new URL(value, base); } catch { return null; } }

export function vaultAttachment(value, base) {
  const url = parseURL(value, base);
  if (!url || url.origin !== new URL(base).origin || url.pathname !== assetPath) return null;
  const path = url.searchParams.get("path");
  if (!path || path === "__missing__") return null;
  return { src: attachmentURL(path), name: path.split("/").pop(), kind: attachmentKind(path), local: true };
}

export function embeddedImage(value, base, alt) {
  if (!value) return null;
  const local = vaultAttachment(value, base);
  if (local) return local.kind === "image" ? local : null;
  const url = parseURL(value, base);
  if (url?.protocol !== "https:" || url.origin === new URL(base).origin) return null;
  return { src: url.href, name: alt || "图片", kind: "image", local: false };
}

// Unlinked inline images get the same keyboard affordance as attachment links.
export function prepareInlineImages(root, base) {
  for (const image of root.querySelectorAll("img[src]")) {
    if (image.closest("a[href]") || !embeddedImage(image.getAttribute("src"), base, image.alt)) continue;
    image.dataset.attachmentPreview = "";
    image.tabIndex = 0;
    image.setAttribute("role", "button");
    image.setAttribute("aria-label", `预览图片：${image.alt || "图片"}`);
  }
}

export class AttachmentPreview {
  constructor(panel) {
    this.panel = panel;
    this.workspace = panel.parentElement;
    this.title = panel.querySelector("h2");
    this.body = panel.querySelector(".attachment-body");
    this.status = panel.querySelector(".attachment-status");
    this.download = panel.querySelector("[download]");
    this.closeButton = panel.querySelector("button");
    this.closeButton.onclick = () => this.close();
  }

  get visible() { return !this.panel.hidden; }

  bind(root) {
    const handle = (event) => {
      const link = event.target.closest("a[href]");
      const image = !link && event.target.closest("img[data-attachment-preview]");
      const media = link ? vaultAttachment(link.getAttribute("href"), location.href)
        : image && embeddedImage(image.getAttribute("src"), location.href, image.alt);
      if (!media) return;
      event.preventDefault();
      this.open(media, link || image);
    };
    root.addEventListener("click", (event) => {
      if (event.button === 0 && !event.defaultPrevented) handle(event);
    }, { capture: true });
    root.addEventListener("keydown", (event) => {
      if (event.target.matches("img[data-attachment-preview]") && ["Enter", " "].includes(event.key)) handle(event);
    });
  }

  openPath(path, source) {
    this.open(vaultAttachment(attachmentURL(path), location.href), source);
  }

  open(media, source) {
    if (!media) return;
    if (source && !this.panel.contains(source)) this.source = source;
    this.title.textContent = media.name;
    this.download.hidden = !media.local;
    if (media.local) this.download.href = media.src;
    else this.download.removeAttribute("href");
    this.status.textContent = "";
    this.body.replaceChildren();
    this.panel.hidden = false;
    this.workspace.classList.add("showing-attachment");
    this.closeButton.focus();
    this.body.scrollTop = 0;

    let element;
    switch (media.kind) {
      case "image":
        element = document.createElement("img");
        element.alt = media.name;
        break;
      case "pdf":
        element = document.createElement("iframe");
        element.title = `PDF 预览：${media.name}`;
        break;
      case "audio": case "video":
        element = document.createElement(media.kind);
        element.controls = true;
        element.preload = "metadata";
        if (media.kind === "video") element.playsInline = true;
        break;
      default:
        this.status.textContent = "不支持预览此文件类型，可下载后查看。";
        return;
    }
    element.addEventListener("error", () => {
      if (element.isConnected) this.status.textContent = media.local
        ? "无法预览此附件，可下载后查看。" : "无法载入图片。";
    });
    element.src = media.src;
    this.body.append(element);
  }

  close(restoreFocus = true) {
    if (!this.visible) return;
    this.panel.hidden = true;
    this.workspace.classList.remove("showing-attachment");
    this.body.replaceChildren();
    if (restoreFocus && this.source?.isConnected && this.source.getClientRects().length) this.source.focus();
    this.source = null;
  }
}
