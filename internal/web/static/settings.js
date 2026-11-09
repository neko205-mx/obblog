const $ = (id) => document.getElementById(id);
const endpoint = "/admin/api/settings";
let previous = null, busy = false, directory = null;

async function request(url, options = {}) {
  const response = await fetch(url, { cache: "no-store", ...options, headers: { "Content-Type": "application/json", ...options.headers } });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `请求失败（${response.status}）`);
  return data;
}

function error(message = "") {
  $("settings-error").textContent = message;
  $("settings-error").hidden = !message;
}

function mode() { return document.querySelector('input[name="mode"]:checked').value; }
function updateSource() {
  const source = mode();
  $("existing-fields").hidden = source !== "existing";
  $("clone-fields").hidden = source !== "clone";
  $("vault-path").required = source === "existing";
  $("vault-path").disabled = source !== "existing";
  $("repository-url").required = source === "clone";
  $("repository-url").disabled = source !== "clone";
  $("repository-branch").disabled = source !== "clone";
  $("credentials").hidden = source === "empty";
  document.querySelector('.settings-nav a[href="#credentials"]').hidden = source === "empty";
}
document.querySelectorAll('input[name="mode"]').forEach((input) => input.onchange = updateSource);
$("git-token").oninput = () => { if ($("git-token").value) $("clear-token").checked = false; };

$("settings-form").onsubmit = async (event) => {
  event.preventDefault();
  if (busy || !previous) return;
  error();
  const password = $("new-password").value;
  const confirmation = $("confirm-password").value;
  const length = new TextEncoder().encode(password).length;
  if (password && (length < 12 || length > 1024)) { error("密码需要 12 至 1024 字节"); $("new-password").focus(); return; }
  if (password !== confirmation) { error("两次密码输入不一致"); $("confirm-password").focus(); return; }
  const input = { mode: mode(), vault: $("vault-path").value.trim(), repository: $("repository-url").value.trim(), branch: $("repository-branch").value.trim(), username: $("git-username").value.trim(), token: $("git-token").value.trim(), clearToken: $("clear-token").checked, password, confirmation };
  if (input.mode === "clone" && !input.token && previous.tokenConfigured && !input.clearToken) { error("更换仓库请重新填写 Token，公开仓库请勾选清除已保存 Token"); $("git-token").focus(); return; }
  busy = true;
  const controls = [...$("settings-form").querySelectorAll("input,button")].map((control) => [control, control.disabled]);
  controls.forEach(([control]) => { control.disabled = true; });
  $("save-settings").disabled = true;
  $("save-settings").textContent = input.mode === "clone" ? "克隆中…" : "保存中…";
  try {
    const result = await request(endpoint, { method: "POST", body: JSON.stringify(input) });
    $("new-password").value = $("confirm-password").value = $("git-token").value = "";
    busy = false;
    location.assign(result.redirect);
  } catch (e) { error(e.message); }
  finally {
    busy = false;
    controls.forEach(([control, disabled]) => { control.disabled = disabled; });
    $("save-settings").disabled = false;
    $("save-settings").textContent = "保存设置";
  }
};

async function browse(path = "") {
  $("directory-error").textContent = "正在读取…";
  try {
    const data = await request(`${endpoint}/directories?${new URLSearchParams({ path })}`);
    directory = data;
    $("directory-location").value = data.path;
    $("directory-list").replaceChildren();
    for (const name of data.directories) {
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = name;
      button.onclick = () => browse(`${data.path.replace(/\/$/, "")}/${name}`);
      $("directory-list").append(button);
    }
    if (!data.directories.length) $("directory-list").textContent = "无子目录";
    $("directory-error").textContent = "";
  } catch (e) { $("directory-error").textContent = e.message; }
}
$("browse-directory").onclick = () => { $("directory-dialog").showModal(); browse($("vault-path").value.trim()); };
$("directory-close").onclick = () => $("directory-dialog").close();
$("directory-form").onsubmit = (event) => { event.preventDefault(); browse($("directory-location").value.trim()); };
$("directory-parent").onclick = () => { if (directory) browse(directory.parent); };
$("directory-select").onclick = () => { if (directory) { $("vault-path").value = directory.path; $("directory-dialog").close(); } };

$("end-test-button").onclick = async () => {
  if (busy) return;
  busy = true;
  $("end-test-button").disabled = true;
  try { await request("/admin/api/test/stop", { method: "POST" }); busy = false; location.replace("about:blank"); }
  catch (e) { error(e.message); busy = false; $("end-test-button").disabled = false; }
};
window.addEventListener("beforeunload", (event) => { if (busy) { event.preventDefault(); event.returnValue = ""; } });

const visibleSections = new Set();
const sections = new IntersectionObserver((entries) => {
  for (const entry of entries) {
    if (entry.isIntersecting) visibleSections.add(entry.target.id);
    else visibleSections.delete(entry.target.id);
  }
  const current = [...document.querySelectorAll(".settings-section")].find((section) => visibleSections.has(section.id));
  if (!current) return;
  document.querySelectorAll(".settings-nav a").forEach((link) => {
    if (link.hash === `#${current.id}`) link.setAttribute("aria-current", "location");
    else link.removeAttribute("aria-current");
  });
}, { rootMargin: "-120px 0px -45% 0px" });

async function initialize() {
  try {
    previous = await request(endpoint);
    $("vault-path").value = previous.vault;
    $("git-username").value = previous.username || "";
    $("save-mode").textContent = previous.git ? "Git 同步" : "本地目录";
    $("token-state").textContent = previous.tokenConfigured ? "已设置" : "未设置";
    $("git-token").placeholder = previous.tokenConfigured ? "留空保留，填写替换" : "公开仓库可留空";
    $("clear-token-label").hidden = !previous.tokenConfigured;
    $("end-test-button").hidden = !previous.testMode;
    updateSource();
    $("settings-form").hidden = false;
    $("save-settings").disabled = false;
    document.querySelectorAll(".settings-section").forEach((section) => sections.observe(section));
    if (previous.problem) error(previous.problem);
  } catch (e) { error(e.message); }
  finally { $("settings-loading").hidden = true; }
}
initialize();
