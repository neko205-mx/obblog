import test from "node:test";
import assert from "node:assert/strict";
import { attachmentKind, attachmentURL, embeddedImage, vaultAttachment } from "../internal/web/static/attachments.mjs";

const base = "http://localhost:8081/admin/?note=Inbox.md";

test("private attachment links keep the encoded path and choose a preview type", () => {
  const path = "00 Inbox/图 #1.PNG";
  assert.deepEqual(vaultAttachment(attachmentURL(path), base), {
    src: attachmentURL(path), name: "图 #1.PNG", kind: "image", local: true,
  });
  assert.equal(attachmentKind("文档.PDF"), "pdf");
  assert.equal(attachmentKind("音频.m4a"), "audio");
  assert.equal(attachmentKind("视频.webm"), "video");
  assert.equal(attachmentKind("png"), "file");
});

test("attachment routing excludes other origins and normal note links", () => {
  for (const link of ["/admin/?note=photo.png", "/admin/api/asset?path=__missing__", "https://other.test/admin/api/asset?path=x.pdf", "/admin/api/note?path=x.pdf", "javascript:alert(1)"]) {
    assert.equal(vaultAttachment(link, base), null, link);
  }
  assert.equal(embeddedImage("/admin/api/asset?path=x.pdf", base), null);
  assert.equal(embeddedImage("/media/x.svg", base), null);
  assert.deepEqual(embeddedImage("https://example.test/image.png", base, "图"), {
    src: "https://example.test/image.png", name: "图", kind: "image", local: false,
  });
});
