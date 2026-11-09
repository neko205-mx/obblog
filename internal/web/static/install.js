const $ = (id) => document.getElementById(id);
let busy = false, directory = null, currentStep = 1;
const endpoint = "/install/api";

async function request(path, options = {}) {
  const response = await fetch(path, { cache: "no-store", ...options, headers: { "Content-Type": "application/json", ...options.headers } });
  const data = await response.json();
  if (!response.ok) throw new Error(data.error || `请求失败（${response.status}）`);
  return data;
}
function error(message = "") { $("setup-error").textContent = message; }
$("end-test-button").onclick = async () => {
  if (busy) return;
  busy = true; $("end-test-button").disabled = true;
  try { await request("/admin/api/test/stop", {method:"POST"}); busy = false; location.replace("about:blank"); }
  catch (e) { error(e.message); busy = false; $("end-test-button").disabled = false; }
};
function step(number) {
  currentStep = number; error();
  $("setup-form").hidden = false;
  $("account-step").hidden = number !== 1;
  $("repository-step").hidden = number !== 2;
  document.querySelectorAll("[data-step]").forEach((item) => item.classList.toggle("active", Number(item.dataset.step) === number));
}
function accountValid() {
  const password = $("setup-password").value;
  const length = new TextEncoder().encode(password).length;
  if (length < 12 || length > 1024) { error("后台密码至少需要 12 字节，最长 1024 字节。"); return false; }
  if (password !== $("setup-confirmation").value) { error("两次密码输入不一致。"); return false; }
  return true;
}
function repositoryMode() { return document.querySelector('input[name="mode"]:checked').value; }
function updateMode() {
  const mode = repositoryMode();
  $("clone-fields").hidden = mode !== "clone";
  $("existing-fields").hidden = mode !== "existing";
  $("repository-url").disabled = mode !== "clone";
  $("repository-branch").disabled = mode !== "clone";
  $("credential-fields").hidden = mode === "empty";
}
$("account-next").onclick = () => { if (accountValid()) { step(2); updateMode(); } };
$("repository-back").onclick = () => { if (!busy) step(1); };
document.querySelectorAll('input[name="mode"]').forEach((input) => input.onchange = updateMode);

$("setup-form").onsubmit = async (event) => {
  event.preventDefault();
  if (busy) return;
  if (currentStep === 1) { if (accountValid()) { step(2); updateMode(); } return; }
  if (!accountValid()) { step(1); accountValid(); return; }
  const mode = repositoryMode();
  if (mode === "clone" && !$("repository-url").value.trim()) { error("请填写 GitHub HTTPS 仓库地址。"); return; }
  if (mode === "existing" && !$("vault-path").value.trim()) { error("请浏览并选择服务器上的笔记目录。"); return; }
  const input = { mode, vault: $("vault-path").value.trim(), repository: $("repository-url").value.trim(), branch: $("repository-branch").value.trim(), username: $("git-username").value.trim(), token: $("git-token").value.trim(), password: $("setup-password").value, confirmation: $("setup-confirmation").value };
  busy = true; error();
  const controls = [...$("setup-form").querySelectorAll("input,button")].map((control) => [control, control.disabled]);
  controls.forEach(([control]) => { control.disabled = true; });
  $("finish-setup").textContent = mode === "clone" ? "克隆中…" : "正在保存配置…";
  try {
    const result = await request(`${endpoint}/configure`, { method: "POST", body: JSON.stringify(input) });
    $("setup-password").value = $("setup-confirmation").value = $("git-token").value = "";
    busy = false;
    location.assign(result.redirect);
  } catch (e) { error(e.message); }
  finally { busy = false; controls.forEach(([control, disabled]) => { control.disabled = disabled; }); $("finish-setup").textContent = "完成安装"; }
};

async function browse(path = "") {
  $("directory-error").textContent = "正在读取…";
  try {
    const data = await request(`${endpoint}/directories?${new URLSearchParams({ path })}`);
    directory = data; $("directory-location").value = data.path;
    const list = $("directory-list"); list.replaceChildren();
    for (const name of data.directories) {
      const button = document.createElement("button");button.type = "button";button.textContent = `▸ ${name}`;
      button.onclick = () => browse(`${data.path.replace(/\/$/, "")}/${name}`);
      list.append(button);
    }
    if (!data.directories.length) list.textContent = "无子目录";
    $("directory-error").textContent = "";
  } catch (e) { $("directory-error").textContent = e.message; }
}
$("browse-directory").onclick = () => { $("directory-dialog").showModal(); browse($("vault-path").value.trim()); };
$("directory-close").onclick = () => $("directory-dialog").close();
$("directory-form").onsubmit = (event) => { event.preventDefault(); browse($("directory-location").value.trim()); };
$("directory-parent").onclick = () => { if (directory) browse(directory.parent); };
$("directory-select").onclick = () => { if (directory) { $("vault-path").value = directory.path; $("directory-dialog").close(); } };
window.addEventListener("beforeunload", (event) => { if (busy) { event.preventDefault(); event.returnValue = ""; } });

async function initialize() {
  try {
    const state = await request("/install/api/state");
    $("end-test-button").hidden = !state.testMode;
    if (state.testMode) $("intro-title").textContent = "测试安装";
    if (state.installed) { location.replace("/admin/login"); return; }
    step(1);
    if (state.testMode && state.sourceVault) {
      document.querySelector('input[name="mode"][value="existing"]').checked = true;
      $("vault-path").value = state.sourceVault;
    }
    if (!state.gitAvailable) {
      document.querySelector('input[name="mode"][value="empty"]').checked = true;
      error("未安装 Git，已选择本地模式");
    }
  } catch (e) { error(e.message); $("setup-login").hidden = false; }
  finally { $("setup-loading").hidden = true; }
}
initialize();
