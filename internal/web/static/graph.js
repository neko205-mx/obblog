import { forceSimulation, forceManyBody, forceLink, forceX, forceY, forceCollide } from "./force.js";
import { selectGraph, nodeRadius } from "./graph-model.mjs";

export class GraphView {
  constructor({ openNote, notify, scopeChanged }) {
    this.openNote = openNote;
    this.notify = notify;
    this.scopeChanged = scopeChanged;
    this.root = document.querySelector("#graph-view");
    this.stage = document.querySelector("#graph-stage");
    this.canvas = document.querySelector("#graph-canvas");
    this.context = this.canvas.getContext("2d");
    this.data = null;
    this.visible = false;
    this.nodes = [];
    this.edges = [];
    this.positions = new Map();
    this.transform = { x: 0, y: 0, k: 1 };
    this.options = { attachments: false, tags: false, unresolved: true, orphans: true, arrows: false, folders: true, depth: 1, query: "", focus: null };
    this.forces = { distance: 100, repel: 140, size: 1 };
    this.loadID = 0;
    this.$("search").addEventListener("input", () => {
      this.options.query = this.$("search").value;
      this.rebuild(true);
    });
    for (const name of ["attachments", "tags", "unresolved", "orphans"]) {
      this.$(name).onchange = () => { this.options[name] = this.$(name).checked; this.rebuild(true); };
    }
    for (const name of ["arrows", "folders"]) {
      this.$(name).onchange = () => { this.options[name] = this.$(name).checked; this.scheduleDraw(); };
    }
    for (const name of ["distance", "repel", "size"]) {
      this.$(name).oninput = () => {
        this.forces[name] = Number(this.$(name).value);
        this.$(`${name}-value`).textContent = String(this.forces[name]);
        this.updateForces();
      };
    }
    this.$("depth").onchange = () => { this.options.depth = Number(this.$("depth").value); this.rebuild(true); };
    this.$("global").onclick = () => this.setScope(false);
    this.$("local").onclick = () => this.setScope(true);
    this.$("refresh").onclick = () => this.load(true);
    this.$("fit").onclick = () => this.fit();
    this.$("zoom-in").onclick = () => this.zoom(1.3);
    this.$("zoom-out").onclick = () => this.zoom(1 / 1.3);
    this.$("picker").onchange = () => {
      const node = this.nodes.find((n) => n.id === this.$("picker").value);
      this.select(node);
      if (node) {
        this.transform.x = this.width / 2 - node.x * this.transform.k;
        this.transform.y = this.height / 2 - node.y * this.transform.k;
        this.scheduleDraw();
      }
    };
    this.$("open").onclick = () => this.activate(this.selected);
    this.canvas.addEventListener("wheel", (event) => {
      event.preventDefault();
      const point = this.point(event);
      this.zoom(Math.exp(-event.deltaY * .0015), point.x, point.y);
    }, { passive: false });
    this.canvas.addEventListener("pointerdown", (event) => this.pointerDown(event));
    this.canvas.addEventListener("pointermove", (event) => this.pointerMove(event));
    this.canvas.addEventListener("pointerup", (event) => this.pointerUp(event));
    this.canvas.addEventListener("pointercancel", () => this.releasePointer());
    this.canvas.addEventListener("lostpointercapture", () => this.releasePointer());
    this.canvas.addEventListener("pointerleave", () => { if (!this.drag) { this.hovered = null; this.scheduleDraw(); } });
    this.canvas.addEventListener("dblclick", (event) => { if (!this.hit(this.point(event))) this.fit(); });
    this.canvas.addEventListener("keydown", (event) => {
      const keys = { ArrowLeft: [35, 0], ArrowRight: [-35, 0], ArrowUp: [0, 35], ArrowDown: [0, -35] };
      if (keys[event.key]) {
        event.preventDefault();
        this.transform.x += keys[event.key][0];
        this.transform.y += keys[event.key][1];
        this.scheduleDraw();
      } else if (event.key === "+" || event.key === "=") { event.preventDefault(); this.zoom(1.3); }
      else if (event.key === "-") { event.preventDefault(); this.zoom(1 / 1.3); }
      else if (event.key === "0") { event.preventDefault(); this.fit(); }
      else if (event.key === "Escape") this.select(null);
    });
    this.resizeObserver = new ResizeObserver(() => this.resize());
    this.resizeObserver.observe(this.stage);
    this.themeObserver = new MutationObserver(() => this.scheduleDraw());
    this.themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["data-theme"] });
    if (window.matchMedia("(max-width: 760px)").matches) this.$("settings").open = false;
  }

  $(name) { return document.querySelector(`#graph-${name}`); }

  async show(current, local = false) {
    this.current = current;
    this.visible = true;
    this.$("local").disabled = !current;
    this.options.focus = local && current ? `file:${current}` : null;
    this.updateScope();
    this.resize();
    if (!this.data) await this.load();
    else this.rebuild(true);
  }

  hide() {
    this.visible = false;
    this.simulation?.stop();
    this.releasePointer();
  }

  invalidate() { this.data = null; }

  async load(force = false) {
    if (!this.visible && !force) return;
    const id = ++this.loadID;
    this.$("refresh").disabled = true;
    this.$("message").hidden = false;
    this.$("message").textContent = "正在解析笔记链接…";
    try {
      const response = await fetch("/admin/api/graph", { cache: "no-store" });
      const data = await response.json();
      if (!response.ok) throw new Error(data.error || "图谱读取失败");
      if (id !== this.loadID) return;
      this.data = data;
      this.$("warnings").replaceChildren();
      this.$("warning-count").textContent = `${data.warnings.length} 条索引提示`;
      this.$("warning-details").hidden = data.warnings.length === 0;
      for (const warning of data.warnings) {
        const item = document.createElement("li");
        item.textContent = `${warning.path}：${warning.message}`;
        this.$("warnings").append(item);
      }
      if (this.visible) this.rebuild(true);
    } catch (error) {
      if (id !== this.loadID) return;
      if (this.data) this.notify(`刷新失败，仍显示旧图谱：${error.message}`);
      else {
        this.$("message").hidden = false;
        this.$("message").textContent = `${error.message}，请点击“重新索引”重试。`;
      }
    } finally {
      if (id === this.loadID) this.$("refresh").disabled = false;
    }
  }

  setScope(local) {
    this.options.focus = local && this.current ? `file:${this.current}` : null;
    this.updateScope();
    this.scopeChanged?.(Boolean(this.options.focus));
    this.rebuild(true);
  }

  updateScope() {
    const local = Boolean(this.options.focus);
    this.$("global").classList.toggle("active", !local);
    this.$("local").classList.toggle("active", local);
    this.$("global").setAttribute("aria-pressed", String(!local));
    this.$("local").setAttribute("aria-pressed", String(local));
    this.$("depth-row").hidden = !local;
    this.$("scope").hidden = !local;
    this.$("scope").textContent = local ? `中心：${this.current}` : "";
  }

  rebuild(fit = false) {
    if (!this.data || !this.visible) return;
    this.simulation?.stop();
    for (const n of this.nodes) this.positions.set(n.id, { x: n.x, y: n.y });
    const graph = selectGraph(this.data, this.options);
    this.nodes = graph.nodes;
    this.edges = graph.edges;
    for (const node of this.nodes) Object.assign(node, this.positions.get(node.id) || {});
    this.neighbors = new Map(this.nodes.map((n) => [n.id, new Set()]));
    for (const edge of this.edges) {
      this.neighbors.get(edge.source).add(edge.target);
      this.neighbors.get(edge.target).add(edge.source);
    }
    this.select(null);
    this.hovered = null;
    this.$("count").textContent = `${this.nodes.length} 个节点 · ${this.edges.length} 条连接`;
    this.$("message").hidden = this.nodes.length > 0;
    this.$("message").textContent = this.data.nodes.length ? "无匹配节点" : "暂无笔记";
    const picker = this.$("picker");
    picker.replaceChildren(new Option("选择节点", ""));
    for (const node of this.nodes) picker.append(new Option(node.path || node.label, node.id));
    this.simulation = forceSimulation(this.nodes).stop().alphaDecay(.035);
    this.configureForces();
    for (let i = 0; i < 100; i++) this.simulation.tick();
    if (fit) this.fit();
    this.simulation.on("tick", () => this.scheduleDraw()).restart();
    this.scheduleDraw();
  }

  configureForces() {
    this.simulation
      .force("charge", forceManyBody().strength(-this.forces.repel).distanceMax(1200))
      .force("links", forceLink(this.edges).id((n) => n.id).distance(this.forces.distance).strength(.18))
      .force("x", forceX(0).strength(.08))
      .force("y", forceY(0).strength(.08))
      .force("collision", forceCollide((n) => nodeRadius(n, this.forces.size) + 7));
  }

  updateForces() {
    if (!this.simulation) return;
    this.configureForces();
    this.simulation.alpha(.5).restart();
    this.scheduleDraw();
  }

  resize() {
    const bounds = this.stage.getBoundingClientRect();
    if (!bounds.width || !bounds.height) return;
    const oldWidth = this.width || bounds.width;
    const oldHeight = this.height || bounds.height;
    this.width = bounds.width;
    this.height = bounds.height;
    this.dpr = Math.min(window.devicePixelRatio || 1, 2);
    this.canvas.width = Math.round(this.width * this.dpr);
    this.canvas.height = Math.round(this.height * this.dpr);
    this.transform.x += (this.width - oldWidth) / 2;
    this.transform.y += (this.height - oldHeight) / 2;
    this.scheduleDraw();
  }

  fit() {
    if (!this.nodes.length) return;
    const xs = this.nodes.map((n) => n.x);
    const ys = this.nodes.map((n) => n.y);
    const minX = Math.min(...xs) - 35, maxX = Math.max(...xs) + 35;
    const minY = Math.min(...ys) - 35, maxY = Math.max(...ys) + 35;
    const settingsWidth = this.$("settings").open && this.width > 650 ? 235 : 0;
    const availableWidth = Math.max(150, this.width - settingsWidth - 70);
    const availableHeight = Math.max(150, this.height - 100);
    const k = Math.max(.08, Math.min(2, availableWidth / (maxX - minX), availableHeight / (maxY - minY)));
    this.transform = { k, x: (this.width - settingsWidth) / 2 - (minX + maxX) / 2 * k, y: this.height / 2 - (minY + maxY) / 2 * k };
    this.scheduleDraw();
  }

  zoom(factor, x = this.width / 2, y = this.height / 2) {
    const t = this.transform;
    const k = Math.max(.06, Math.min(7, t.k * factor));
    t.x = x - (x - t.x) * k / t.k;
    t.y = y - (y - t.y) * k / t.k;
    t.k = k;
    this.scheduleDraw();
  }

  point(event) {
    const bounds = this.canvas.getBoundingClientRect();
    return { x: event.clientX - bounds.left, y: event.clientY - bounds.top };
  }

  hit(point) {
    const { x, y, k } = this.transform;
    const wx = (point.x - x) / k, wy = (point.y - y) / k;
    let match = null, closest = Infinity;
    for (const node of this.nodes) {
      const distance = Math.hypot(node.x - wx, node.y - wy);
      if (distance < Math.max(this.radius(node), 8 / k) && distance < closest) { match = node; closest = distance; }
    }
    return match;
  }

  pointerDown(event) {
    if (event.button !== 0 || this.drag) return;
    const point = this.point(event);
    this.drag = { id: event.pointerId, point, previous: point, node: this.hit(point), moved: false };
    this.canvas.setPointerCapture(event.pointerId);
    this.canvas.focus({ preventScroll: true });
    if (this.drag.node) {
      this.drag.node.fx = this.drag.node.x;
      this.drag.node.fy = this.drag.node.y;
    }
  }

  pointerMove(event) {
    const point = this.point(event);
    if (this.drag && this.drag.id === event.pointerId) {
      const drag = this.drag;
      if (Math.hypot(point.x - drag.point.x, point.y - drag.point.y) > 4) drag.moved = true;
      if (drag.moved && drag.node) {
        drag.node.fx = (point.x - this.transform.x) / this.transform.k;
        drag.node.fy = (point.y - this.transform.y) / this.transform.k;
        this.simulation.alphaTarget(.15).restart();
      } else if (drag.moved) {
        this.transform.x += point.x - drag.previous.x;
        this.transform.y += point.y - drag.previous.y;
      }
      drag.previous = point;
    } else this.hovered = this.hit(point);
    this.canvas.style.cursor = this.drag ? "grabbing" : this.hovered ? "pointer" : "grab";
    this.scheduleDraw();
  }

  pointerUp(event) {
    if (!this.drag || this.drag.id !== event.pointerId) return;
    const { node, moved } = this.drag;
    this.releasePointer();
    if (!moved) {
      this.select(node);
      if (node) this.activate(node);
    }
  }

  releasePointer() {
    if (this.drag?.node) { this.drag.node.fx = null; this.drag.node.fy = null; }
    this.drag = null;
    this.simulation?.alphaTarget(0);
  }

  select(node) {
    this.selected = node;
    this.$("selection").hidden = !node;
    if (node) {
      this.$("selection-label").textContent = node.label;
      this.$("selection-path").textContent = node.path || (node.kind === "tag" ? "标签节点" : "尚未创建的笔记");
      this.$("selection-stats").textContent = `${node.incoming} 条入链 · ${node.outgoing} 条出链 · ${node.degree} 个邻居`;
      this.$("open").hidden = node.kind !== "note" && node.kind !== "asset";
      this.$("open").textContent = node.kind === "asset" ? "打开附件 ↗" : "打开笔记 ↗";
      this.$("picker").value = node.id;
    } else this.$("picker").value = "";
    this.scheduleDraw();
  }

  activate(node) {
    if (!node) return;
    if (node.kind === "note") this.openNote(node.path);
    else if (node.kind === "asset") window.open(`/admin/api/asset?${new URLSearchParams({ path: node.path })}`, "_blank", "noopener,noreferrer");
    else if (node.kind === "tag") {
      this.$("search").value = node.label;
      this.options.query = node.label;
      this.rebuild(true);
    } else this.notify("笔记尚未创建");
  }

  scheduleDraw() {
    if (this.frame || !this.visible) return;
    this.frame = requestAnimationFrame(() => { this.frame = null; if (this.visible) this.draw(); });
  }

  radius(node) {
    const k = this.transform.k;
    return Math.max(2.3 / k, nodeRadius(node, this.forces.size) / Math.sqrt(k));
  }

  draw() {
    if (!this.width || !this.height) return;
    const ctx = this.context, t = this.transform;
    const css = getComputedStyle(document.documentElement);
    const accent = css.getPropertyValue("--accent").trim();
    const muted = css.getPropertyValue("--muted").trim();
    const text = css.getPropertyValue("--text").trim();
    const palette = [accent, "#ab9870", "#7ea3b7", "#bc8c7d", "#91a56c", "#b5a58b", "#719d99"];
    const active = this.hovered || this.selected;
    const neighborhood = active ? this.neighbors.get(active.id) : null;
    ctx.setTransform(this.dpr, 0, 0, this.dpr, 0, 0);
    ctx.clearRect(0, 0, this.width, this.height);
    ctx.translate(t.x, t.y);
    ctx.scale(t.k, t.k);
    for (const edge of this.edges) {
      const a = edge.source, b = edge.target;
      const highlighted = active && (a.id === active.id || b.id === active.id);
      ctx.globalAlpha = active ? highlighted ? .85 : .08 : .38;
      ctx.strokeStyle = highlighted ? accent : muted;
      ctx.lineWidth = ((highlighted ? 1.5 : .65) + Math.min(1.5, Math.log1p(edge.count) * .3)) / Math.sqrt(t.k);
      ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x, b.y); ctx.stroke();
      if (this.options.arrows) {
        const angle = Math.atan2(b.y - a.y, b.x - a.x);
        const radius = this.radius(b) + 3;
        const x = b.x - Math.cos(angle) * radius, y = b.y - Math.sin(angle) * radius;
        const length = 5 / Math.sqrt(t.k);
        ctx.beginPath(); ctx.moveTo(x - Math.cos(angle - .45) * length, y - Math.sin(angle - .45) * length); ctx.lineTo(x, y); ctx.lineTo(x - Math.cos(angle + .45) * length, y - Math.sin(angle + .45) * length); ctx.stroke();
      }
    }
    const labels = [];
    for (const node of this.nodes) {
      const connected = !active || node.id === active.id || neighborhood?.has(node.id);
      ctx.globalAlpha = connected ? 1 : .16;
      let color = accent;
      if (this.options.folders && node.path?.includes("/")) {
        let hash = 0;
        for (const c of node.path.split("/")[0]) hash = (hash * 31 + c.codePointAt(0)) >>> 0;
        color = palette[hash % palette.length];
      }
      if (node.kind === "unresolved") color = muted;
      else if (node.kind === "tag") color = "#bb9d68";
      else if (node.kind === "asset") color = "#7f9fb2";
      const radius = this.radius(node);
      ctx.fillStyle = color; ctx.strokeStyle = color; ctx.lineWidth = 1.1 / Math.sqrt(t.k);
      ctx.beginPath(); ctx.arc(node.x, node.y, radius, 0, Math.PI * 2);
      if (node.kind === "unresolved") ctx.stroke(); else ctx.fill();
      if (node.id === this.options.focus || node.id === active?.id) {
        ctx.beginPath(); ctx.arc(node.x, node.y, radius + 4 / Math.sqrt(t.k), 0, Math.PI * 2); ctx.stroke();
      }
      const showLabel = active ? connected : this.nodes.length < 85 || t.k > .8 || node.incoming > 1;
      if (showLabel) {
        labels.push({ node, radius, priority: node.id === active?.id ? Infinity : node.incoming });
      }
    }
    const occupied = [];
    ctx.font = `${10 / t.k}px ${css.getPropertyValue("--sans")}`;
    ctx.textAlign = "center";
    for (const { node, radius } of labels.sort((a, b) => b.priority - a.priority)) {
      const label = node.label.length > 26 && node.id !== active?.id ? node.label.slice(0, 25) + "…" : node.label;
      const width = ctx.measureText(label).width;
      const x = node.x - width / 2, y = node.y + radius + 13 / t.k;
      const rect = { x, y: y - 10 / t.k, width, height: 14 / t.k };
      const overlap = occupied.some((r) => rect.x < r.x + r.width && rect.x + rect.width > r.x && rect.y < r.y + r.height && rect.y + rect.height > r.y);
      if (overlap && node.id !== active?.id) continue;
      occupied.push(rect);
      ctx.globalAlpha = active ? 1 : .85;
      ctx.fillStyle = node.id === active?.id ? accent : text;
      ctx.fillText(label, node.x, y);
    }
    ctx.globalAlpha = 1;
    this.$("zoom-value").textContent = `${Math.round(t.k * 100)}%`;
    this.$("hover").textContent = active ? `${active.path || active.label} · ${active.degree} 个邻居` : "拖动空白处平移 · 滚轮缩放 · 点击节点阅读";
  }
}
