(() => {
  "use strict";
  let csrf = "";
  const notice = document.getElementById("notice");

  function showNotice(message, isError = false) {
    notice.textContent = message;
    notice.hidden = !message;
    notice.classList.toggle("error", isError);
  }

  async function api(path, options = {}) {
    const response = await fetch(path, options);
    if (response.status === 401) {
      window.location.assign("/admin/login");
      throw new Error("Session expired");
    }
    const body = await response.json();
    if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
    return body;
  }

  function mutation(method, path, body) {
    return api(path, {
      method,
      headers: {"Content-Type": "application/json", "X-CSRF-Token": csrf},
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  }

  function cell(row, text) {
    const td = document.createElement("td");
    td.textContent = text === null || text === undefined || text === "" ? "—" : String(text);
    row.appendChild(td);
    return td;
  }

  function table(target, columns, rows, action) {
    target.replaceChildren();
    if (!rows.length) {
      const empty = document.createElement("p");
      empty.className = "empty";
      empty.textContent = "Nothing to display.";
      target.appendChild(empty);
      return;
    }
    const element = document.createElement("table");
    const head = document.createElement("thead");
    const header = document.createElement("tr");
    for (const column of columns) {
      const th = document.createElement("th");
      th.textContent = column;
      header.appendChild(th);
    }
    if (action) {
      const th = document.createElement("th");
      th.textContent = "Actions";
      header.appendChild(th);
    }
    head.appendChild(header);
    element.appendChild(head);
    const body = document.createElement("tbody");
    rows.forEach((item, index) => {
      const row = document.createElement("tr");
      action ? action(row, item, index) : item.forEach((value) => cell(row, value));
      body.appendChild(row);
    });
    element.appendChild(body);
    target.appendChild(element);
  }

  async function loadDiagnostics() {
    const data = await api("/admin/diagnostics");
    const states = document.getElementById("job-states");
    states.replaceChildren();
    const entries = Object.entries(data.jobStates || {});
    if (!entries.length) entries.push(["accepted", 0]);
    for (const [state, count] of entries) {
      const card = document.createElement("div");
      card.className = "stat";
      const strong = document.createElement("strong");
      strong.textContent = count;
      const label = document.createElement("span");
      label.textContent = state.replaceAll("_", " ");
      card.append(strong, label);
      states.appendChild(card);
    }
    table(document.getElementById("diagnostic-cache"), ["Section", "Cached as of", "Records"], (data.cache || []).map((item) => [item.section, item.cachedAsOf || "No snapshot", item.recordCount]));
    table(document.getElementById("diagnostic-jobs"), ["Job", "Printer", "State", "Attempts", "Updated", "Last error"], (data.jobs || []).map((job) => [job.jobKey, job.printerId, job.state, job.attempts, job.updatedAt, job.lastError]));
  }

  async function loadUsers() {
    const data = await api("/admin/users");
    table(document.getElementById("users-list"), ["Username", "Role", "Created"], data.users || [], (row, user) => {
      cell(row, user.username);
      cell(row, user.role);
      cell(row, user.createdAt);
      const actions = cell(row, "");
      const button = document.createElement("button");
      button.type = "button";
      button.className = "danger";
      button.textContent = "Remove";
      button.addEventListener("click", async () => {
        if (!window.confirm(`Remove fallback access for ${user.username}?`)) return;
        try {
          await mutation("DELETE", `/admin/users?username=${encodeURIComponent(user.username)}`);
          showNotice(`Removed ${user.username}.`);
          await loadUsers();
        } catch (error) { showNotice(error.message, true); }
      });
      actions.appendChild(button);
    });
  }

  async function loadConfig() {
    const data = await api("/admin/config");
    document.getElementById("config-version").textContent = `Applied config version ${data.configVersion}. Configuration changes are made in ORRN, never on the LAN fallback listener.`;
    table(document.getElementById("config-printers"), ["Printer ID", "Name", "Host", "Port", "Stock", "DPI"], data.printers || [], (row, printer) => {
      cell(row, printer.printerId);
      cell(row, printer.name);
      cell(row, printer.host);
      cell(row, printer.port);
      cell(row, `${printer.labelWidthMm || 100} × ${printer.labelHeightMm || 75} mm`);
      cell(row, printer.dpi || 203);
      const actions = cell(row, "");
      const button = document.createElement("button");
      button.type = "button";
      button.textContent = "Test print";
      button.addEventListener("click", async () => {
        if (!window.confirm(`Queue a test label on ${printer.name}?`)) return;
        try {
          await mutation("POST", `/admin/printers/${encodeURIComponent(printer.printerId)}/test`);
          showNotice(`Test label queued for ${printer.name}.`);
        } catch (error) { showNotice(error.message, true); }
      });
      actions.appendChild(button);
    });
  }

  document.querySelectorAll(".tab[data-panel]").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".tab[data-panel]").forEach((item) => item.classList.toggle("active", item === tab));
      document.querySelectorAll(".panel").forEach((panel) => { panel.hidden = panel.id !== `panel-${tab.dataset.panel}`; });
    });
  });
  document.getElementById("refresh-diagnostics").addEventListener("click", () => loadDiagnostics().catch((error) => showNotice(error.message, true)));
  document.getElementById("refresh-config").addEventListener("click", () => loadConfig().catch((error) => showNotice(error.message, true)));
  document.getElementById("user-form").addEventListener("submit", async (event) => {
    event.preventDefault();
    try {
      await mutation("POST", "/admin/users", {
        username: document.getElementById("new-username").value,
        password: document.getElementById("new-password").value,
        role: document.getElementById("new-role").value,
      });
      showNotice("Fallback user saved. Existing sessions remain valid until expiry or removal.");
      event.target.reset();
      await loadUsers();
    } catch (error) { showNotice(error.message, true); }
  });
  document.getElementById("logout").addEventListener("click", async () => {
    try { await mutation("POST", "/admin/logout"); } finally { window.location.assign("/admin/login"); }
  });

  (async () => {
    try {
      const session = await api("/admin/session");
      csrf = session.csrfToken;
      document.getElementById("session-user").textContent = `${session.username} · ${session.role}`;
      await Promise.all([loadDiagnostics(), loadUsers(), loadConfig()]);
    } catch (error) { showNotice(error.message, true); }
  })();
})();
