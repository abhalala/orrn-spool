(() => {
  "use strict";
  const form = document.getElementById("login-form");
  const message = document.getElementById("login-message");
  form.addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = form.querySelector("button[type=submit]");
    button.disabled = true;
    message.textContent = "";
    const scope = form.dataset.scope;
    try {
      const response = await fetch(`/${scope}/login`, {
        method: "POST",
        headers: {"Content-Type": "application/json"},
        body: JSON.stringify({
          username: document.getElementById("username").value,
          password: document.getElementById("password").value,
        }),
      });
      const body = await response.json();
      if (!response.ok) throw new Error(body.error || "Sign-in failed");
      window.location.assign(scope === "admin" ? "/admin" : "/fallback/");
    } catch (error) {
      message.textContent = "Sign-in failed. Check the local username and password.";
      document.getElementById("password").select();
    } finally {
      button.disabled = false;
    }
  });
})();
