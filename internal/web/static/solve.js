// The solution editor: a textarea, a save, and a test run.
//
// A textarea rather than a real code editor is deliberate — the site ships no
// third-party JavaScript, and the server gofmts every save, so indentation
// comes back correct without anything here having to understand Go.

(() => {
  "use strict";

  const root = document.querySelector("[data-solve]");
  if (!root) return;

  const slug = root.dataset.slug;
  const editor = root.querySelector("[data-editor]");
  const output = root.querySelector("[data-output]");
  const state = root.querySelector("[data-state]");
  const saveBtn = root.querySelector("[data-save]");
  const runBtn = root.querySelector("[data-run]");
  const createBtn = root.querySelector("[data-create]");

  // The guard on the server refuses anything that is not JSON from this origin,
  // so every call goes through here.
  async function post(path, body) {
    const res = await fetch(`/problems/${encodeURIComponent(slug)}/${path}`, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body || {}),
    });
    if (!res.ok) throw new Error((await res.text()).trim() || `HTTP ${res.status}`);
    return res.json();
  }

  function say(text, kind) {
    if (!state) return;
    state.textContent = text;
    state.dataset.kind = kind || "";
  }

  // ---- create ----

  if (createBtn) {
    createBtn.addEventListener("click", async () => {
      createBtn.disabled = true;
      say("creating…");
      try {
        await post("create");
        // The page renders a different layout once the files exist, and
        // re-requesting it is simpler than building that layout twice.
        location.reload();
      } catch (err) {
        createBtn.disabled = false;
        say(err.message, "bad");
      }
    });
  }

  if (!editor) return;

  // ---- dirty tracking ----

  let saved = editor.value;
  const dirty = () => editor.value !== saved;

  function markClean(code) {
    if (typeof code === "string" && code !== editor.value) {
      // gofmt reformatted it; keep the caret roughly where it was rather than
      // letting it jump to the top.
      const at = editor.selectionStart;
      editor.value = code;
      editor.selectionStart = editor.selectionEnd = Math.min(at, code.length);
    }
    saved = editor.value;
  }

  editor.addEventListener("input", () => {
    if (dirty()) say("unsaved");
  });

  window.addEventListener("beforeunload", (e) => {
    if (!dirty()) return;
    e.preventDefault();
    e.returnValue = "";
  });

  // ---- save and run ----

  async function save() {
    say("saving…");
    const res = await post("solution", { code: editor.value });
    markClean(res.code);
    if (res.formatError) say(`saved, but it does not parse: ${res.formatError}`, "warn");
    else say("saved", "ok");
    return res;
  }

  function show(res) {
    if (!output) return;
    output.hidden = false;
    const parts = [];
    if (res.runError) parts.push(res.runError);
    if (res.output) parts.push(res.output);
    output.textContent = parts.join("\n\n") || "(no output)";
    output.dataset.kind = res.passed ? "ok" : "bad";
  }

  async function run() {
    say("running…");
    if (runBtn) runBtn.disabled = true;
    try {
      // The server saves before it runs, so what is tested is what is on screen.
      const res = await post("test", { code: editor.value });
      markClean(res.code);
      show(res);
      if (res.runError) say(res.runError, "bad");
      else if (res.formatError) say(`does not parse: ${res.formatError}`, "warn");
      else say(res.passed ? `passed in ${res.millis} ms` : "failed", res.passed ? "ok" : "bad");
    } finally {
      if (runBtn) runBtn.disabled = false;
    }
  }

  const guarded = (fn) => async () => {
    try {
      await fn();
    } catch (err) {
      say(err.message, "bad");
    }
  };

  saveBtn?.addEventListener("click", guarded(save));
  runBtn?.addEventListener("click", guarded(run));

  // ---- editing affordances ----

  function replaceSelection(text) {
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    editor.setRangeText(text, start, end, "end");
    editor.dispatchEvent(new Event("input"));
  }

  editor.addEventListener("keydown", (e) => {
    const mod = e.metaKey || e.ctrlKey;

    if (mod && e.key === "s") {
      e.preventDefault();
      guarded(save)();
      return;
    }
    if (mod && e.key === "Enter") {
      e.preventDefault();
      guarded(run)();
      return;
    }
    if (mod) return;

    if (e.key === "Tab") {
      // Trapping Tab costs keyboard users the ability to leave the field, so
      // Escape-then-Tab is the way out and Shift+Tab still moves backwards.
      if (e.shiftKey) return;
      e.preventDefault();
      replaceSelection("\t");
      return;
    }

    if (e.key === "Enter") {
      // Carry the current line's indentation onto the next one, and add a level
      // after an opening brace. gofmt fixes the rest on save.
      const at = editor.selectionStart;
      const lineStart = editor.value.lastIndexOf("\n", at - 1) + 1;
      const line = editor.value.slice(lineStart, at);
      const indent = (line.match(/^[\t ]*/) || [""])[0];
      const deeper = /[{(\[]\s*$/.test(line) ? "\t" : "";
      e.preventDefault();
      replaceSelection("\n" + indent + deeper);
    }
  });

  say(dirty() ? "unsaved" : "");
})();
