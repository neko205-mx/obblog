import test from "node:test";
import assert from "node:assert/strict";
import { selectGraph, nodeRadius } from "../internal/web/static/graph-model.mjs";

const graph = {
  nodes: [
    { id: "a", kind: "note", label: "A", path: "notes/A.md", tags: ["topic/child"] },
    { id: "b", kind: "note", label: "B", path: "notes/B.md" },
    { id: "c", kind: "note", label: "C", path: "other/C.md" },
    { id: "o", kind: "note", label: "Orphan", path: "Orphan.md" },
    { id: "i", kind: "asset", label: "Image", path: "image.png" },
    { id: "t", kind: "tag", label: "#topic/child" },
    { id: "u", kind: "unresolved", label: "Missing" },
  ],
  edges: [
    { source: "b", target: "a", count: 2 }, { source: "b", target: "c", count: 1 },
    { source: "a", target: "i", count: 1 }, { source: "a", target: "t", count: 1 }, { source: "c", target: "u", count: 1 },
  ],
};

test("defaults hide assets/tags and preserve orphans and unresolved notes", () => {
  const selected = selectGraph(graph);
  assert.deepEqual(selected.nodes.map((n) => n.id), ["a", "b", "c", "o", "u"]);
  assert.equal(selected.edges.length, 3);
  assert.equal(selected.nodes.find((n) => n.id === "a").incoming, 1);
});

test("local graph walks incoming and outgoing edges with bounded depth", () => {
  assert.deepEqual(selectGraph(graph, { focus: "a", depth: 1 }).nodes.map((n) => n.id), ["a", "b"]);
  assert.deepEqual(selectGraph(graph, { focus: "a", depth: 2 }).nodes.map((n) => n.id), ["a", "b", "c"]);
  assert.deepEqual(selectGraph(graph, { focus: "a", depth: 3 }).nodes.map((n) => n.id), ["a", "b", "c", "u"]);
  assert.equal(selectGraph(graph, { focus: "nonexistent" }).nodes.length, 0);
});

test("orphan filtering is computed after asset/tag filters", () => {
  const selected = selectGraph(graph, { orphans: false, unresolved: false });
  assert.deepEqual(selected.nodes.map((n) => n.id), ["a", "b", "c"]);
  assert.deepEqual(selectGraph(graph, { orphans: false, focus: "o" }).nodes.map((n) => n.id), ["o"]);
});

test("path, name, and nested-tag searches", () => {
  assert.deepEqual(selectGraph(graph, { query: "path:other" }).nodes.map((n) => n.id), ["c"]);
  assert.deepEqual(selectGraph(graph, { query: "#topic" }).nodes.map((n) => n.id), ["a"]);
  assert.deepEqual(selectGraph(graph, { query: "tag:topic/child", tags: true }).nodes.map((n) => n.id), ["a", "t"]);
  assert.deepEqual(selectGraph(graph, { query: "Orphan" }).nodes.map((n) => n.id), ["o"]);
});

test("data passed to a force simulation is isolated from original graph data", () => {
  const before = structuredClone(graph);
  const selected = selectGraph(graph, { attachments: true, tags: true });
  selected.nodes[0].x = 300;
  selected.edges[0].source = selected.nodes[0];
  assert.deepEqual(graph, before);
  assert.ok(nodeRadius({ incoming: 10 }) > nodeRadius({ incoming: 0 }));
});
