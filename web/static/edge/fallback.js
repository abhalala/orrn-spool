(() => {
  "use strict";
  let csrf = "";
  let role = "viewer";
  const notice = document.getElementById("notice");

  function showNotice(message, isError = false) {
    notice.textContent = message;
    notice.hidden = !message;
    notice.classList.toggle("error", isError);
  }

  async function api(path, options = {}) {
    const response = await fetch(path, options);
    if (response.status === 401) {
      window.location.assign("/fallback/login");
      throw new Error("Session expired");
    }
    const body = await response.json();
    if (!response.ok) throw new Error(body.error || `Request failed (${response.status})`);
    return body;
  }

  function mutation(path) {
    return api(path, {method: "POST", headers: {"X-CSRF-Token": csrf}});
  }

  function cell(row, text) {
    const td = document.createElement("td");
    td.textContent = text === null || text === undefined || text === "" ? "—" : String(text);
    row.appendChild(td);
    return td;
  }

  function empty(target, message = "No cached records.") {
    target.replaceChildren();
    const item = document.createElement("p");
    item.className = "empty";
    item.textContent = message;
    target.appendChild(item);
  }

  function primitiveFields(value, prefix = "", output = []) {
    if (!value || typeof value !== "object" || Array.isArray(value)) return output;
    for (const [key, item] of Object.entries(value)) {
      const label = prefix ? `${prefix}.${key}` : key;
      if (item !== null && typeof item !== "object") output.push([label, item]);
      else if (item && !Array.isArray(item) && output.length < 12) primitiveFields(item, label, output);
    }
    return output;
  }

  function renderRecords(section, data, append) {
    document.getElementById(`cache-${section}`).textContent = data.banner || (data.cachedAsOf ? `Cached as of ${data.cachedAsOf}` : "No cached snapshot");
    const target = document.getElementById(`records-${section}`);
    if (!append) target.replaceChildren();
    target.querySelector(".load-more")?.remove();
    const records = data.records || [];
    if (!records.length) {
      if (!append) empty(target);
      return;
    }
    for (const record of records) {
      const detail = document.createElement("details");
      detail.className = "record";
      const summary = document.createElement("summary");
      const name = document.createElement("span");
      name.textContent = record.sortKey || record.entityId;
      const id = document.createElement("span");
      id.textContent = record.entityId;
      summary.append(name, id);
      detail.appendChild(summary);
      const fields = primitiveFields(record.dto).slice(0, 18);
      if (fields.length) {
        const list = document.createElement("dl");
        for (const [key, value] of fields) {
          const group = document.createElement("div");
          const term = document.createElement("dt");
          term.textContent = key;
          const description = document.createElement("dd");
          description.textContent = typeof value === "boolean" ? (value ? "Yes" : "No") : String(value);
          group.append(term, description);
          list.appendChild(group);
        }
        detail.appendChild(list);
      }
      const raw = document.createElement("pre");
      raw.textContent = JSON.stringify(record.dto, null, 2);
      detail.appendChild(raw);
      target.appendChild(detail);
    }
    if (data.nextOffset !== null && data.nextOffset !== undefined) {
      const more = document.createElement("button");
      more.type = "button";
      more.className = "load-more";
      more.textContent = `Load more (${records.length + data.offset} of ${data.total})`;
      more.addEventListener("click", () => loadSection(section, data.nextOffset, true).catch((error) => showNotice(error.message, true)));
      target.appendChild(more);
    }
  }

  async function loadSection(section, offset = 0, append = false) {
    const data = await api(`/fallback/${section}?limit=100&offset=${encodeURIComponent(offset)}`);
    renderRecords(section, data, append);
    if (section === "stock" && !append) {
      const summary = document.getElementById("summary-stock");
      summary.textContent = data.summaries && Object.keys(data.summaries).length ? `Summary: ${JSON.stringify(data.summaries)}` : "";
    }
  }

  async function loadJobs() {
    const data = await api("/fallback/jobs");
    const target = document.getElementById("local-jobs");
    target.replaceChildren();
    const jobs = data.jobs || [];
    if (!jobs.length) {
      empty(target, "No local print jobs.");
      return;
    }
    const table = document.createElement("table");
    const head = document.createElement("thead");
    const header = document.createElement("tr");
    for (const label of ["Job", "Printer", "State", "Copies", "Attempts", "Updated", "Source", "Actions"]) {
      const th = document.createElement("th"); th.textContent = label; header.appendChild(th);
    }
    head.appendChild(header); table.appendChild(head);
    const body = document.createElement("tbody");
    for (const job of jobs) {
      const row = document.createElement("tr");
      cell(row, job.jobKey); cell(row, job.printerId); cell(row, job.state); cell(row, job.copies);
      cell(row, job.attempts); cell(row, job.updatedAt); cell(row, job.sourceJobId ? "Offline reprint" : "Cloud / test");
      const actions = cell(row, "");
      if (job.canReprint && (role === "printer" || role === "admin")) {
        const button = document.createElement("button");
        button.type = "button"; button.textContent = "Reprint";
        button.addEventListener("click", async () => {
          const warning = job.state === "sent" || job.state === "outcome_unknown" ? " This can produce another physical label." : "";
          if (!window.confirm(`Queue a new reprint attempt for ${job.jobKey}?${warning}`)) return;
          try {
            await mutation(`/fallback/jobs/${encodeURIComponent(job.id)}/reprint`);
            showNotice("Offline reprint queued as a new local job.");
            await loadJobs();
          } catch (error) { showNotice(error.message, true); }
        });
        actions.appendChild(button);
      }
      body.appendChild(row);
    }
    table.appendChild(body); target.appendChild(table);
  }

  async function loadPrinters() {
    const data = await api("/fallback/printers");
    const target = document.getElementById("printers-list");
    target.replaceChildren();
    const printers = data.printers || [];
    if (!printers.length) {
      empty(target, "No printers in the applied configuration.");
      return;
    }
    for (const printer of printers) {
      const card = document.createElement("article");
      card.className = "card printer-card";
      const title = document.createElement("h3"); title.textContent = printer.name;
      const status = document.createElement("p"); status.className = "status"; status.textContent = printer.status;
      const seen = document.createElement("p"); seen.className = "muted"; seen.textContent = `Last device response: ${printer.lastSeenAt || "never"}`;
      card.append(title, status, seen);
      if (printer.lastError) {
        const error = document.createElement("p"); error.textContent = printer.lastError; card.appendChild(error);
      }
      if (role === "printer" || role === "admin") {
        const button = document.createElement("button"); button.type = "button"; button.textContent = "Test print";
        button.addEventListener("click", async () => {
          if (!window.confirm(`Queue a test label on ${printer.name}?`)) return;
          try {
            await mutation(`/fallback/printers/${encodeURIComponent(printer.printerId)}/test`);
            showNotice(`Test label queued for ${printer.name}.`);
            await loadJobs();
          } catch (error) { showNotice(error.message, true); }
        });
        card.appendChild(button);
      }
      target.appendChild(card);
    }
  }

  document.querySelectorAll(".tab[data-panel]").forEach((tab) => {
    tab.addEventListener("click", () => {
      document.querySelectorAll(".tab[data-panel]").forEach((item) => item.classList.toggle("active", item === tab));
      document.querySelectorAll(".panel").forEach((panel) => { panel.hidden = panel.id !== `panel-${tab.dataset.panel}`; });
    });
  });
  document.querySelectorAll(".cache-panel").forEach((panel) => {
    panel.querySelector(".refresh-section").addEventListener("click", () => loadSection(panel.dataset.section).catch((error) => showNotice(error.message, true)));
  });
  document.getElementById("refresh-jobs").addEventListener("click", () => loadJobs().catch((error) => showNotice(error.message, true)));
  document.getElementById("refresh-printers").addEventListener("click", () => loadPrinters().catch((error) => showNotice(error.message, true)));
  document.getElementById("logout").addEventListener("click", async () => {
    try { await mutation("/fallback/logout"); } finally { window.location.assign("/fallback/login"); }
  });

  (async () => {
    try {
      const session = await api("/fallback/session");
      csrf = session.csrfToken;
      role = session.role;
      document.getElementById("session-user").textContent = `${session.username} · ${session.role}`;
      await Promise.all([loadSection("stock"), loadSection("orders"), loadSection("dies"), loadSection("print_status"), loadJobs(), loadPrinters()]);
    } catch (error) { showNotice(error.message, true); }
  })();
})();
