export function selectGraph(data, options = {}) {
  const settings = { attachments: false, tags: false, unresolved: true, orphans: true, depth: 1, query: "", ...options };
  const query = settings.query.trim().toLocaleLowerCase();
  const includesTag = (node, tag) => (node.tags || []).some((t) => t.toLocaleLowerCase() === tag || t.toLocaleLowerCase().startsWith(`${tag}/`)) || (node.kind === "tag" && node.label.toLocaleLowerCase() === `#${tag}`);
  const matches = (node) => {
    if (!query) return true;
    if (query.startsWith("path:")) return (node.path || "").toLocaleLowerCase().includes(query.slice(5).replace(/^"|"$/g, ""));
    if (query.startsWith("tag:")) return includesTag(node, query.slice(4).replace(/^#/, ""));
    if (query.startsWith("#")) return includesTag(node, query.slice(1));
    return `${node.label}\n${node.path || ""}`.toLocaleLowerCase().includes(query);
  };
  let nodes = data.nodes.filter((n) => {
    if (n.kind === "asset" && !settings.attachments) return false;
    if (n.kind === "tag" && !settings.tags) return false;
    if (n.kind === "unresolved" && !settings.unresolved) return false;
    return matches(n);
  });
  let ids = new Set(nodes.map((n) => n.id));
  let edges = data.edges.filter((e) => ids.has(e.source) && ids.has(e.target));
  if (settings.focus) {
    const neighbors = new Map(nodes.map((n) => [n.id, []]));
    for (const e of edges) {
      neighbors.get(e.source).push(e.target);
      neighbors.get(e.target).push(e.source);
    }
    const visited = new Set(ids.has(settings.focus) ? [settings.focus] : []);
    let frontier = [...visited];
    const depth = Math.max(1, Math.min(3, Number(settings.depth) || 1));
    for (let i = 0; i < depth; i++) {
      const next = [];
      for (const id of frontier) for (const neighbor of neighbors.get(id) || []) {
        if (!visited.has(neighbor)) { visited.add(neighbor); next.push(neighbor); }
      }
      frontier = next;
    }
    nodes = nodes.filter((n) => visited.has(n.id));
    edges = edges.filter((e) => visited.has(e.source) && visited.has(e.target));
  }
  const connected = new Set(edges.flatMap((e) => [e.source, e.target]));
  if (!settings.orphans) nodes = nodes.filter((n) => connected.has(n.id) || n.id === settings.focus);
  // Each filtering pass returns fresh objects: d3-force mutates node/edge inputs.
  const byID = new Map(nodes.map((n) => [n.id, { ...n, incoming: 0, outgoing: 0, degree: 0 }]));
  const neighbors = new Map(nodes.map((n) => [n.id, new Set()]));
  for (const edge of edges) {
    byID.get(edge.source).outgoing++;
    byID.get(edge.target).incoming++;
    neighbors.get(edge.source).add(edge.target);
    neighbors.get(edge.target).add(edge.source);
  }
  for (const [id, node] of byID) node.degree = neighbors.get(id).size;
  return { nodes: [...byID.values()], edges: edges.map((e) => ({ ...e })) };
}

export function nodeRadius(node, multiplier = 1) {
  return (3.5 + Math.min(11, Math.sqrt(node.incoming || 0) * 1.8)) * multiplier;
}
