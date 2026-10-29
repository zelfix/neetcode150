// Client-side filtering and keyboard navigation for the problem list.
// The server renders every row; this only hides and highlights.

(() => {
  "use strict";

  const bar = document.querySelector("[data-filters]");
  const rows = Array.from(document.querySelectorAll(".row[data-title]"));
  const search = document.querySelector("[data-search]");
  const counter = document.querySelector("[data-count]");
  const sections = Array.from(document.querySelectorAll("[data-group-section]"));

  // active holds the selected values per filter group, e.g. {difficulty: Set}.
  const active = new Map();

  function matches(row) {
    for (const [group, values] of active) {
      if (values.size === 0) continue;
      if (!values.has(row.dataset[group])) return false;
    }
    const q = (search?.value || "").trim().toLowerCase();
    if (!q) return true;
    return row.dataset.title.includes(q) || row.dataset.id.startsWith(q);
  }

  function apply() {
    let shown = 0;
    for (const row of rows) {
      const ok = matches(row);
      row.classList.toggle("hidden", !ok);
      if (ok) shown++;
    }
    // Hide a pattern whose every row is filtered out, so the page has no
    // orphaned headings.
    for (const section of sections) {
      const any = section.querySelector(".row:not(.hidden)");
      section.classList.toggle("hidden", !any);
    }
    if (counter) {
      counter.textContent = shown === rows.length ? "" : `${shown} of ${rows.length}`;
    }
    moveCursor(0, true);
  }

  if (bar) {
    for (const chip of bar.querySelectorAll(".chip")) {
      const group = chip.closest("[data-group]").dataset.group;
      if (!active.has(group)) active.set(group, new Set());
      chip.addEventListener("click", () => {
        const values = active.get(group);
        const on = chip.getAttribute("aria-pressed") === "true";
        if (on) values.delete(chip.dataset.value);
        else values.add(chip.dataset.value);
        chip.setAttribute("aria-pressed", String(!on));
        apply();
      });
    }
    search?.addEventListener("input", apply);
  }

  // ---- keyboard navigation ----

  let cursor = -1;

  function visible() {
    return rows.filter((r) => !r.classList.contains("hidden"));
  }

  function moveCursor(delta, keepInRange) {
    const list = visible();
    for (const row of rows) row.classList.remove("cursor");
    if (list.length === 0) {
      cursor = -1;
      return;
    }
    if (keepInRange) {
      cursor = Math.min(cursor, list.length - 1);
      if (cursor < 0) return;
    } else {
      cursor = Math.max(0, Math.min(cursor + delta, list.length - 1));
    }
    const row = list[cursor];
    row.classList.add("cursor");
    row.scrollIntoView({ block: "nearest" });
  }

  document.addEventListener("keydown", (e) => {
    const typing = /^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement?.tagName || "");
    if (e.key === "/" && !typing) {
      e.preventDefault();
      search?.focus();
      search?.select();
      return;
    }
    if (e.key === "Escape" && typing) {
      document.activeElement.blur();
      return;
    }
    if (typing || e.metaKey || e.ctrlKey || e.altKey) return;

    if (e.key === "j" || e.key === "ArrowDown") {
      if (rows.length === 0) return;
      e.preventDefault();
      moveCursor(cursor < 0 ? 0 : 1);
    } else if (e.key === "k" || e.key === "ArrowUp") {
      if (rows.length === 0) return;
      e.preventDefault();
      moveCursor(-1);
    } else if (e.key === "Enter") {
      const row = visible()[cursor];
      if (row) {
        const href = row.getAttribute("href") || row.querySelector("a")?.getAttribute("href");
        if (href) window.location.href = href;
      }
    }
  });

  apply();
})();
