document.querySelector("#login-form").addEventListener("submit", async (event) => {
  event.preventDefault();
  const button = document.querySelector("#login-button");
  button.disabled = true;
  document.querySelector("#login-error").textContent = "";
  try {
    const response = await fetch("/admin/api/login", { method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ password: document.querySelector("#password").value }) });
    const data = await response.json();
    if (!response.ok) throw new Error(data.error);
    location.assign("/admin/");
  } catch (error) { document.querySelector("#login-error").textContent = error.message || "登录失败"; }
  finally { button.disabled = false; }
});
