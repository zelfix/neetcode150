// The solution editor: a textarea, a save, and a test run.
//
// A textarea rather than a real code editor is deliberate — the site ships no
// third-party JavaScript. Drafts save verbatim; explicit test runs format Go
// without changing code while the user types.

(() => {
  "use strict";

  const root = document.querySelector("[data-solve]");
  if (!root) return;

  const slug = root.dataset.slug;
  // Declared up here because the re-solve handler clears it before the editor
  // section runs.
  let saved = "";
  const editor = root.querySelector("[data-editor]");
  const output = root.querySelector("[data-output]");
  const state = root.querySelector("[data-state]");
  const runBtn = root.querySelector("[data-run]");
  const submitBtn = root.querySelector("[data-submit]");
  const cancelBtn = root.querySelector("[data-cancel]");
  let awaitingSubmitRating = submitBtn?.dataset.pending === "true";
  let testsPassedForCurrent = false;
  let reviewCompleted = root.dataset.active === "false";
  // One Start button per language, each carrying its own.
  const startBtns = Array.from(root.querySelectorAll("[data-start]"));

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

  const notesForm = root.querySelector("[data-notes-form]");
  const notesInput = root.querySelector("[data-notes-input]");
  const notesState = root.querySelector("[data-notes-state]");
  let savedNotes = notesInput?.value || "";
  let notesSaving = null;
  let notesTimer = null;
  const notesDirty = () => notesInput && notesInput.value !== savedNotes;
  const notesError = (err) => { if (notesState) notesState.textContent = `Not saved: ${err.message}`; };

  function scheduleNotes() {
    if (notesTimer) clearTimeout(notesTimer);
    notesTimer = window.setTimeout(() => {
      notesTimer = null;
      saveNotes().catch(notesError);
    }, 800);
  }

  async function saveNotes() {
    if (notesTimer) clearTimeout(notesTimer);
    notesTimer = null;
    while (notesSaving) await notesSaving;
    if (!notesDirty()) return;
    const snapshot = notesInput.value;
    notesState.textContent = "saving…";
    notesSaving = post("notes", { text: snapshot }).then(() => {
      savedNotes = snapshot;
      notesState.textContent = notesDirty() ? "unsaved" : "saved";
    });
    try { await notesSaving; } finally {
      notesSaving = null;
      if (notesInput.value !== snapshot && notesDirty()) scheduleNotes();
    }
  }

  notesForm?.addEventListener("submit", (e) => {
    e.preventDefault();
    saveNotes().catch(notesError);
  });
  notesInput?.addEventListener("input", () => {
    notesState.textContent = notesDirty() ? "unsaved" : "saved";
    scheduleNotes();
  });
  notesInput?.addEventListener("blur", () => saveNotes().catch(notesError));
  notesInput?.addEventListener("keydown", (e) => {
    if ((e.metaKey || e.ctrlKey) && e.key === "s") {
      e.preventDefault();
      saveNotes().catch(notesError);
    }
  });
  window.addEventListener("beforeunload", (e) => {
    if (!notesDirty()) return;
    e.preventDefault();
    e.returnValue = "";
  });

  function activateTab(name, focus = false) {
    for (const tab of root.querySelectorAll("[data-work-tab]")) {
      const selected = tab.dataset.workTab === name;
      tab.setAttribute("aria-selected", String(selected));
      tab.tabIndex = selected ? 0 : -1;
      if (selected && focus) tab.focus();
    }
    for (const panel of root.querySelectorAll("[data-work-panel]")) panel.hidden = panel.dataset.workPanel !== name;
    if (name !== "notes") saveNotes().catch(notesError);
  }
  const tabs = [...root.querySelectorAll("[data-work-tab]")];
  tabs.forEach((tab, index) => {
    tab.addEventListener("click", () => activateTab(tab.dataset.workTab));
    tab.addEventListener("keydown", (e) => {
      let next;
      if (e.key === "ArrowRight") next = (index + 1) % tabs.length;
      else if (e.key === "ArrowLeft") next = (index + tabs.length - 1) % tabs.length;
      else if (e.key === "Home") next = 0;
      else if (e.key === "End") next = tabs.length - 1;
      else return;
      e.preventDefault();
      activateTab(tabs[next].dataset.workTab, true);
    });
  });
  activateTab(window.location?.hash === "#notes" ? "notes" : "solution");

  async function reloadWorkspace(tab = "solution") {
    if (notesInput) notesInput.readOnly = true;
    try {
      await saveNotes();
      window.history?.replaceState(null, "", tab === "notes" ? "#notes" : window.location.pathname + window.location.search);
      location.reload();
    } catch (err) {
      if (notesInput) notesInput.readOnly = false;
      notesError(err);
      throw err;
    }
  }

  let clockInterval = null;
  const clock = root.querySelector("[data-clock]");
  if (clock) {
    let seconds = Number(clock.dataset.seconds) || 0;
    const paintClock = () => {
      const hours = Math.floor(seconds / 3600);
      const minutes = Math.floor((seconds % 3600) / 60);
      const remainder = seconds % 60;
      clock.textContent = [hours, minutes, remainder].map((part) => String(part).padStart(2, "0")).join(":");
    };
    paintClock();
    // During an interview the page keeps the clock and reports the limit
    // passing, once. The server holds the flag that makes it once: a reload
    // starts a new interval here, and the interviewer must not be told twice.
    const head = root.querySelector("[data-interview]");
    const limit = head ? Number(head.dataset.limit) * 60 : 0;
    let reported = head ? head.dataset.said === "true" : true;
    // post() is rooted at this problem; the interview endpoints are not.
    const reportTimeUp = async () => {
      reported = true;
      try {
        const res = await fetch("/interview/timeup", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: "{}",
        });
        if (!res.ok) throw new Error(`HTTP ${res.status}`);
      } catch {
        reported = false; // a failed report is retried on the next tick
      }
    };
    clockInterval = window.setInterval(() => {
      seconds += 1;
      paintClock();
      if (!reported && limit > 0 && seconds >= limit) {
        void reportTimeUp();
      }
    }, 1000);

    // The interview can move without this page doing anything: the interviewer
    // closes a problem itself once it is satisfied. Follow it rather than
    // leaving the editor open on a problem that is over.
    if (head) {
      const id = head.dataset.interview;
      window.setInterval(async () => {
        let st;
        try {
          const res = await fetch("/interview/state", { headers: { Accept: "application/json" } });
          if (!res.ok) return;
          st = await res.json();
        } catch {
          return; // a dropped poll is not news; the next one will say
        }
        if (!st.live || st.finishing) {
          window.location.assign(`/interview/${st.id || id}`);
          return;
        }
        if (st.slug && st.slug !== slug) {
          window.location.assign(`/problems/${encodeURIComponent(st.slug)}/solve`);
        }
      }, 4000);
    }
  }

  // ---- create ----

  for (const btn of startBtns) {
    btn.addEventListener("click", async () => {
      // Disable every one of them: two attempts started at once would archive
      // the first before it had a chance to exist.
      for (const other of startBtns) other.disabled = true;
      say("starting a new attempt…");
      try {
        if (notesInput) notesInput.readOnly = true;
        await saveNotes();
        await post("start", { lang: btn.dataset.lang || "" });
        await reloadWorkspace();
      } catch (err) {
        // Back to how they started: a language whose tests cannot run here
        // stays disabled, the rest become clickable again.
        for (const other of startBtns) other.disabled = !!other.dataset.why;
        if (notesInput) notesInput.readOnly = false;
        say(err.message, "bad");
      }
    });
  }

  if (cancelBtn) {
    cancelBtn.addEventListener("click", async () => {
      cancelBtn.disabled = true;
      say("saving and stopping the timer…");
      try {
        await beginAction();
        await post("cancel", { code: editor.value });
        saved = editor.value;
        await reloadWorkspace();
      } catch (err) {
        cancelBtn.disabled = false;
        say(err.message, "bad");
      } finally {
        endAction();
      }
    });
  }

  if (awaitingSubmitRating) {
    say("Answer the tutor's follow-up in the chat. The tutor will ask again if needed, then rate automatically.", "ok");
    if (editor) editor.readOnly = true;
    if (runBtn) runBtn.disabled = true;
    if (submitBtn) submitBtn.disabled = true;
  }

  if (submitBtn) {
    submitBtn.addEventListener("click", async () => {
      submitBtn.disabled = true;
      if (runBtn) runBtn.disabled = true;
      if (cancelBtn) cancelBtn.disabled = true;
      say("running tests and asking the tutor…");
      try {
        await beginAction();
        const result = await post("submit", { code: editor.value });
        editor.value = result.code;
        saved = result.code;
        if (output) {
          output.hidden = false;
          output.textContent = result.output || result.runError || "Tests finished.";
        }
        if (result.runError) {
          testsPassedForCurrent = false;
          say(`Tests could not complete: ${result.runError}`, "bad");
          submitBtn.disabled = true;
          if (runBtn) runBtn.disabled = false;
          if (cancelBtn) cancelBtn.disabled = false;
          return;
        }
        if (result.ratingError) {
          say(`Tutor step failed: ${result.ratingError}`, "bad");
          submitBtn.disabled = !testsPassedForCurrent;
          if (runBtn) runBtn.disabled = awaitingSubmitRating;
          if (cancelBtn) cancelBtn.disabled = false;
          return;
        }
        if (result.pending) {
          // Submit may have started the tutor session after this page loaded,
          // so reload to attach to its replayable transcript.
          say("Tests finished. Loading the tutor's follow-up…", "ok");
          await reloadWorkspace();
          return;
        }
        if (!result.passed) testsPassedForCurrent = false;
        awaitingSubmitRating = false;
        say(`Rated ${result.confidence}/5 · hint ${result.rung}; saved ${result.archive}`, "ok");
        await reloadWorkspace();
      } catch (err) {
        say(err.message, "bad");
        submitBtn.disabled = !testsPassedForCurrent;
        if (runBtn) runBtn.disabled = awaitingSubmitRating;
        if (cancelBtn) cancelBtn.disabled = false;
      } finally {
        endAction();
      }
    });
  }

  // ---- the tutor panel ----

  // Replies are rendered with DOM nodes and textContent only — never
  // innerHTML. The text comes from a model reading this repository, and the
  // safe way to render untrusted-ish text is not to parse it as markup at all.
  // Fenced code blocks are the one exception worth handling, because an
  // unformatted Go snippet in a narrow column is unreadable.
  const tutor = document.querySelector("[data-tutor]");
  if (tutor) {
    const log = tutor.querySelector("[data-tutor-log]");
    const input = tutor.querySelector("[data-tutor-input]");
    const form = tutor.querySelector("[data-tutor-form]");
    const meta = tutor.querySelector("[data-tutor-meta]");
    const tState = tutor.querySelector("[data-tutor-state]");
    const stopBtn = tutor.querySelector("[data-tutor-stop]");
    const resetBtn = tutor.querySelector("[data-tutor-reset]");
    const sendBtn = tutor.querySelector("[data-tutor-send]");

    let stream = null;
    let bubble = null; // the assistant turn being written
    let raw = ""; // its text so far, kept for the code-fence pass

    function tSay(text, kind) {
      if (!tState) return;
      tState.textContent = text || "";
      tState.dataset.kind = kind || "";
    }

    const atBottom = () =>
      !log || log.scrollHeight - log.scrollTop - log.clientHeight < 40;

    function scroll(wasAtBottom) {
      // Only follow the text if the reader had not scrolled up to re-read
      // something earlier in the conversation.
      if (log && wasAtBottom) log.scrollTop = log.scrollHeight;
    }

    function add(cls, text) {
      const el = document.createElement("div");
      el.className = cls;
      if (text) el.textContent = text;
      const wasAtBottom = atBottom();
      log.appendChild(el);
      scroll(wasAtBottom);
      return el;
    }

    // Inline marks within one run of prose: **bold** and `code`. Built out of
    // elements and text nodes, so the only thing a reply can do is be read.
    function inline(target, text) {
      const marks = /\*\*([^*]+)\*\*|`([^`]+)`/g;
      let at = 0;
      let m;
      while ((m = marks.exec(text)) !== null) {
        if (m.index > at) target.appendChild(document.createTextNode(text.slice(at, m.index)));
        const el = document.createElement(m[1] ? "strong" : "code");
        el.textContent = m[1] || m[2];
        target.appendChild(el);
        at = marks.lastIndex;
      }
      if (at < text.length) target.appendChild(document.createTextNode(text.slice(at)));
    }

    // Prose between fences. Bullet and numbered lines become a real list; the
    // rest keeps its line breaks, which the bubble preserves with pre-wrap.
    function prose(target, text) {
      let para = [];
      let list = null;

      const flush = () => {
        const body = para.join("\n").replace(/^\n+|\n+$/g, "");
        para = [];
        if (!body) return;
        const span = document.createElement("span");
        inline(span, body);
        target.appendChild(span);
      };

      for (const line of text.split("\n")) {
        const item = line.match(/^\s*(?:[-*]|\d+\.)\s+(.*)$/);
        if (!item) {
          list = null;
          para.push(line);
          continue;
        }
        flush();
        if (!list) {
          list = document.createElement("ul");
          target.appendChild(list);
        }
        const li = document.createElement("li");
        inline(li, item[1]);
        list.appendChild(li);
      }
      flush();
    }

    // Rebuild a finished reply, splitting fenced blocks out into <pre>.
    function format(el, text) {
      el.textContent = "";
      const parts = text.split(/```/);
      parts.forEach((part, i) => {
        if (i % 2 === 1) {
          const pre = document.createElement("pre");
          const code = document.createElement("code");
          // The first line of a fence is the language, not content.
          code.textContent = part.replace(/^[a-zA-Z0-9+#-]*\n/, "");
          pre.appendChild(code);
          el.appendChild(pre);
        } else if (part) {
          prose(el, part);
        }
      });
    }

    function render(e) {
      switch (e.kind) {
        case "ready":
          if (meta) {
            const tools = (e.tools || []).length;
            meta.textContent = [tutor.dataset.provider, e.model, e.mode, tools ? `${tools} tools` : ""]
              .filter(Boolean)
              .join(" · ");
          }
          break;
        case "user":
          bubble = null;
          raw = "";
          // A turn with no display text is one the application sent on the
          // user's behalf — the prompt that opens the lesson, for one. The
          // reply to it belongs on screen; the prompt does not.
          if (e.text) add("tutor-turn is-user", e.text);
          break;
        case "text": {
          const follow = atBottom();
          if (!bubble) { bubble = add("tutor-turn is-tutor", ""); raw = ""; }
          raw += e.text;
          bubble.textContent = raw;
          scroll(follow);
          break;
        }
        case "internal-user":
        case "internal-text":
          break;
        case "submission-question":
          bubble = null;
          raw = "";
          add("tutor-turn is-tutor", e.text);
          break;
        case "submission-done": {
          const follow = atBottom();
          bubble = null;
          raw = "";
          format(add("tutor-turn is-tutor", ""), e.text);
          if (root.dataset.pendingSubmit === "true" && e.seq > Number(root.dataset.pendingSeq || 0)) {
            reviewCompleted = true;
            awaitingSubmitRating = false;
            root.dataset.pendingSubmit = "false";
            say("Rating saved. You can keep discussing the solution in the chat.", "ok");
            if (clockInterval) window.clearInterval(clockInterval);
            if (editor) editor.readOnly = true;
            if (runBtn) runBtn.disabled = true;
            if (cancelBtn) cancelBtn.disabled = true;
            busy(false);
            if (input) { input.disabled = false; input.placeholder = "Continue discussing your solution — ⌘↵ to send"; }
            if (sendBtn) sendBtn.disabled = false;
            if (resetBtn) resetBtn.disabled = false;
            const studyPresets = tutor.querySelector("[data-study-presets]");
            const reviewPresets = tutor.querySelector("[data-review-presets]");
            if (studyPresets) studyPresets.hidden = true;
            if (reviewPresets) reviewPresets.hidden = false;
            const finish = document.createElement("button");
            finish.type = "button";
            finish.className = "review-finish";
            finish.textContent = "Open notes";
            finish.addEventListener("click", async () => { try { await reloadWorkspace("notes"); } catch (err) { say(err.message, "bad"); } });
            log.appendChild(finish);
            scroll(follow);
          }
          break;
        }
        case "tool":
          add("tutor-note is-tool", e.detail ? `${e.tool} — ${e.detail}` : e.tool);
          break;
        case "denied":
          add("tutor-note is-denied", e.text);
          break;
        case "done":
          if (bubble) format(bubble, raw);
          bubble = null;
          tSay(e.cost ? `$${e.cost.toFixed(4)} so far` : "", "ok");
          busy(false);
          break;
        case "error":
          if (bubble) format(bubble, raw);
          bubble = null;
          add("tutor-note is-error", e.text);
          tSay("", "");
          busy(false);
          break;
        case "exit":
          add("tutor-note is-error", e.text);
          bubble = null;
          busy(false);
          closeStream();
          break;
      }
    }

    function busy(on) {
      if (sendBtn) sendBtn.disabled = on;
      if (stopBtn) stopBtn.hidden = !on;
      if (on) tSay("thinking…");
    }

    function closeStream() {
      if (stream) {
        stream.close();
        stream = null;
      }
    }

    function openStream() {
      if (stream) return;
      stream = new EventSource(`/problems/${encodeURIComponent(slug)}/tutor/events`);
      stream.onmessage = (ev) => {
        try {
          render(JSON.parse(ev.data));
        } catch {
          /* one unreadable event is not worth breaking the panel for */
        }
      };
      // EventSource retries forever by default. There is no session to follow
      // after a reset, so stop instead of hammering the server.
      stream.onerror = () => closeStream();
    }

    async function ask(text) {
      text = (text || "").trim();
      if (!text) return;
      busy(true);
      try {
        await post("tutor", { text });
        openStream();
      } catch (err) {
        busy(false);
        add("tutor-note is-error", err.message);
      }
    }

    form?.addEventListener("submit", (e) => {
      e.preventDefault();
      const text = input.value;
      input.value = "";
      ask(text);
    });

    input?.addEventListener("keydown", (e) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "Enter") {
        e.preventDefault();
        const text = input.value;
        input.value = "";
        ask(text);
      }
    });

    for (const chip of tutor.querySelectorAll("[data-ask]")) {
      chip.addEventListener("click", () => ask(chip.dataset.ask));
    }

    stopBtn?.addEventListener("click", async () => {
      try {
        await post("tutor/interrupt");
        tSay("stopped", "warn");
      } catch (err) {
        tSay(err.message, "bad");
      }
    });

    resetBtn?.addEventListener("click", async () => {
      if (!confirm("Start a new tutor session? This conversation is discarded.")) return;
      closeStream();
      try {
        await post("tutor/reset");
        if (log) {
          for (const child of [...log.children]) if (!child.hasAttribute("data-statement-message")) child.remove();
          log.scrollTop = 0;
        }
        bubble = null;
        raw = "";
        if (meta) meta.textContent = "";
        busy(false);
        tSay("new session", "ok");
      } catch (err) {
        tSay(err.message, "bad");
      }
    });

    // A session already running means there is a conversation to replay.
    if (tutor.dataset.active === "true") { log.scrollTop = log.scrollHeight; openStream(); }
  }

  if (!editor) return;

  // ---- dirty tracking ----

  saved = editor.value;
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
    testsPassedForCurrent = false;
    if (submitBtn && !awaitingSubmitRating) submitBtn.disabled = true;
    if (dirty()) {
      say("unsaved");
      scheduleAutosave();
    }
  });

  window.addEventListener("beforeunload", (e) => {
    if (!dirty()) return;
    e.preventDefault();
    e.returnValue = "";
  });

  // ---- save and run ----
  //
  // Saving is not a decision, so it is not a button. Both buttons that remain
  // save on the server before they act. Background saves keep a verbatim
  // snapshot and never replace text or move the caret.
  const autosaveDelay = 1200;
  let autosaveTimer = null;
  let saveInFlight = null;
  let acting = false;

  function scheduleAutosave() {
    if (autosaveTimer) clearTimeout(autosaveTimer);
    autosaveTimer = window.setTimeout(() => {
      autosaveTimer = null;
      if (reviewCompleted || awaitingSubmitRating || acting || saveInFlight || !dirty() || !editor.value.trim()) return;
      guarded(save)();
    }, autosaveDelay);
  }

  function clearAutosave() {
    if (autosaveTimer) clearTimeout(autosaveTimer);
    autosaveTimer = null;
  }

  function save() {
    clearAutosave();
    if (reviewCompleted || acting || awaitingSubmitRating || !editor.value.trim()) return;
    if (saveInFlight) return saveInFlight;
    const snapshot = editor.value;
    say("saving…");
    saveInFlight = post("solution", { code: snapshot }).then((res) => {
      saved = snapshot;
      say(dirty() ? "unsaved" : "saved", dirty() ? "" : "ok");
      return res;
    }).finally(() => {
      saveInFlight = null;
      if (editor.value !== snapshot && dirty() && !acting) scheduleAutosave();
    });
    return saveInFlight;
  }

  async function beginAction() {
    acting = true;
    editor.readOnly = true;
    clearAutosave();
    // Finish an older draft write before an explicit action saves newer code.
    if (saveInFlight) await saveInFlight.catch(() => {});
  }

  function endAction() {
    acting = false;
    editor.readOnly = awaitingSubmitRating || reviewCompleted;
    if (dirty() && !awaitingSubmitRating && !reviewCompleted) scheduleAutosave();
  }

  // Rebuilt from the run response rather than left as the page rendered it: a
  // comparison computed at page load is stale the moment the editor is touched.
  // Elements and text nodes only, like the tutor log — the lines are the
  // reader's own code, and they go in as text either way.
  function renderDiff(d) {
    const box = root.querySelector("[data-diff]");
    if (!box) return;
    box.replaceChildren();
    if (!d) return;
    const details = document.createElement("details");
    details.className = "submission";
    const summary = document.createElement("summary");
    summary.textContent = d.tooBig || d.identical
      ? `Changes since ${d.against}`
      : `Changes since ${d.against} · +${d.added} −${d.removed}`;
    details.appendChild(summary);
    const note = (text) => {
      const p = document.createElement("p");
      p.className = "panel-note";
      p.textContent = text;
      details.appendChild(p);
    };
    if (d.tooBig) {
      note("Those files are too large to compare.");
    } else if (d.identical) {
      note("Identical, line for line. Worth asking whether that is memory or understanding.");
    } else {
      const pre = document.createElement("pre");
      pre.className = "source-code";
      const code = document.createElement("code");
      for (const line of d.lines || []) {
        const span = document.createElement("span");
        span.className = line.class;
        span.textContent = line.sign + line.text;
        code.appendChild(span);
        code.appendChild(document.createTextNode("\n"));
      }
      pre.appendChild(code);
      details.appendChild(pre);
    }
    if (d.skipped) note(`${d.skipped} archived in another language, not compared.`);
    box.appendChild(details);
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
    if (acting || awaitingSubmitRating || reviewCompleted) return;
    say("running…");
    if (runBtn) runBtn.disabled = true;
    if (cancelBtn) cancelBtn.disabled = true;
    testsPassedForCurrent = false;
    if (submitBtn) submitBtn.disabled = true;
    try {
      await beginAction();
      // The server saves before it runs, so what is tested is what is on screen.
      const res = await post("test", { code: editor.value });
      markClean(res.code);
      show(res);
      testsPassedForCurrent = res.passed && !res.runError;
      if (submitBtn) submitBtn.disabled = !testsPassedForCurrent;
      if (testsPassedForCurrent) renderDiff(res.diff);
      if (res.runError) say(res.runError, "bad");
      else if (res.formatError) say(`does not parse: ${res.formatError}`, "warn");
      else if (!res.passed) say("failed", "bad");
      else say(res.diff ? `passed in ${res.millis} ms · comparison in History` : `passed in ${res.millis} ms`, "ok");
    } finally {
      if (runBtn) runBtn.disabled = false;
      if (cancelBtn) cancelBtn.disabled = false;
      if (!testsPassedForCurrent && submitBtn) submitBtn.disabled = true;
      endAction();
    }
  }

  const guarded = (fn) => async () => {
    try {
      await fn();
    } catch (err) {
      say(err.message, "bad");
    }
  };

  runBtn?.addEventListener("click", guarded(run));

  // ---- editing affordances ----

  function replaceSelection(text) {
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    editor.setRangeText(text, start, end, "end");
    editor.dispatchEvent(new Event("input"));
  }

  // One indent unit is the language's: a tab for Go, four spaces for Python.
  const indentUnit = () => editor.dataset.indent || "\t";

  // The span of whole lines the selection touches, which is what Tab and
  // Shift+Tab act on when more than one line is involved.
  function selectedLines() {
    const from = editor.value.lastIndexOf("\n", editor.selectionStart - 1) + 1;
    let to = editor.value.indexOf("\n", editor.selectionEnd);
    if (to === -1) to = editor.value.length;
    return { from, to, text: editor.value.slice(from, to) };
  }

  function shiftLines(by) {
    const unit = indentUnit();
    const { from, to, text } = selectedLines();
    const lines = text.split("\n");
    let firstDelta = 0;
    let total = 0;
    const out = lines.map((line, i) => {
      if (by > 0) {
        if (i === 0) firstDelta = unit.length;
        total += unit.length;
        return unit + line;
      }
      // Outdent takes one unit, or whatever part of it is actually there, so a
      // line indented with a stray pair of spaces still moves left.
      let cut = 0;
      if (line.startsWith(unit)) {
        cut = unit.length;
      } else {
        while (cut < unit.length && cut < line.length && (line[cut] === " " || line[cut] === "\t")) cut++;
      }
      if (i === 0) firstDelta = -cut;
      total -= cut;
      return line.slice(cut);
    });
    const start = editor.selectionStart;
    const end = editor.selectionEnd;
    editor.setRangeText(out.join("\n"), from, to, "preserve");
    editor.selectionStart = Math.max(from, start + firstDelta);
    editor.selectionEnd = Math.max(from, end + total);
    editor.dispatchEvent(new Event("input"));
  }

  const pairs = { "(": ")", "[": "]", "{": "}", '"': '"', "'": "'", "`": "`" };
  const closers = new Set(Object.values(pairs));

  // Whether the caret sits between the two halves of a pair. Both characters
  // have to exist: past either end of the text they read as undefined, and
  // pairs[undefined] === undefined would call that a match.
  function insidePair(at) {
    if (at <= 0 || at >= editor.value.length) return false;
    const open = editor.value[at - 1];
    return pairs[open] !== undefined && pairs[open] === editor.value[at];
  }

  async function formatNow() {
    if (editor.readOnly || !editor.value.trim()) return;
    const res = await post("format", { code: editor.value });
    if (res.why) {
      say(res.why, "warn");
      return;
    }
    if (res.error) {
      say(`does not parse: ${res.error}`, "warn");
      return;
    }
    if (res.code === editor.value) {
      say(`${res.tool}: already tidy`);
      return;
    }
    // Keep the caret where it was in the text, measured from the end so that
    // re-indentation above it does not drag it off the line it was on.
    const tail = editor.value.length - editor.selectionStart;
    editor.value = res.code;
    const at = Math.max(0, res.code.length - tail);
    editor.selectionStart = editor.selectionEnd = at;
    editor.dispatchEvent(new Event("input"));
    say(`${res.tool} applied`, "ok");
  }

  editor.addEventListener("keydown", (e) => {
    const mod = e.metaKey || e.ctrlKey;
    if (editor.readOnly) return;

    if (mod && e.shiftKey && (e.key === "f" || e.key === "F")) {
      e.preventDefault();
      guarded(formatNow)();
      return;
    }
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

    const unit = indentUnit();
    const at = editor.selectionStart;
    const selected = editor.value.slice(at, editor.selectionEnd);

    // Tab is trapped for indentation, which would otherwise make this the only
    // field a keyboard user cannot leave. Escape is the way out: it releases
    // focus, and Tab moves on from wherever focus lands.
    if (e.key === "Escape") {
      editor.blur();
      return;
    }

    if (e.key === "Tab") {
      e.preventDefault();
      if (selected.includes("\n")) {
        shiftLines(e.shiftKey ? -1 : 1);
      } else if (e.shiftKey) {
        shiftLines(-1);
      } else {
        replaceSelection(unit);
      }
      return;
    }

    // Typing an opening bracket or quote closes it. With text selected it wraps
    // the selection instead, which is the other thing people reach for.
    if (pairs[e.key] !== undefined) {
      // A quote is also an apostrophe, and `don't` should not grow a second
      // one. Only pair a quote when what follows cannot continue a word.
      const quote = e.key === pairs[e.key];
      const next = editor.value[editor.selectionEnd] || "";
      if (quote && !selected && /[\w"'`]/.test(next)) return;
      e.preventDefault();
      if (selected) {
        editor.setRangeText(e.key + selected + pairs[e.key], at, editor.selectionEnd, "preserve");
        editor.selectionStart = at + 1;
        editor.selectionEnd = at + 1 + selected.length;
        editor.dispatchEvent(new Event("input"));
        return;
      }
      editor.setRangeText(e.key + pairs[e.key], at, at, "preserve");
      editor.selectionStart = editor.selectionEnd = at + 1;
      editor.dispatchEvent(new Event("input"));
      return;
    }

    // Typing the closing half of a pair the editor just added steps over it
    // rather than doubling it.
    if (closers.has(e.key) && !selected && editor.value[at] === e.key) {
      e.preventDefault();
      editor.selectionStart = editor.selectionEnd = at + 1;
      return;
    }

    // Backspace inside an empty pair takes both halves.
    if (e.key === "Backspace" && !selected && insidePair(at)) {
      e.preventDefault();
      editor.setRangeText("", at - 1, at + 1, "end");
      editor.dispatchEvent(new Event("input"));
      return;
    }

    if (e.key === "Enter") {
      // Carry the current line's indentation onto the next one, and add a level
      // after an opening bracket or a block-opening colon. Between the halves
      // of a pair, the closing half goes to a line of its own.
      const lineStart = editor.value.lastIndexOf("\n", at - 1) + 1;
      const line = editor.value.slice(lineStart, at);
      const indent = (line.match(/^[\t ]*/) || [""])[0];
      const opened = /[{(\[:]\s*$/.test(line);
      const inPair = insidePair(at);
      e.preventDefault();
      if (inPair) {
        editor.setRangeText("\n" + indent + unit + "\n" + indent, at, at, "preserve");
        editor.selectionStart = editor.selectionEnd = at + 1 + indent.length + unit.length;
        editor.dispatchEvent(new Event("input"));
        return;
      }
      replaceSelection("\n" + indent + (opened ? unit : ""));
    }
  });

  say(dirty() ? "unsaved" : "");
})();
