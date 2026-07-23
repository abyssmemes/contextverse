(function () {
  var KEY = "contextverse-theme";

  function current() {
    return document.documentElement.getAttribute("data-theme") === "dark" ? "dark" : "light";
  }

  function apply(theme) {
    // Dark flips only canvas CSS variables; sidebar gradient is theme-invariant.
    document.documentElement.setAttribute("data-theme", theme === "dark" ? "dark" : "light");
  }

  function persist(theme) {
    try {
      localStorage.setItem(KEY, theme);
    } catch (_) {}
  }

  function load() {
    try {
      var saved = localStorage.getItem(KEY);
      if (saved === "dark") {
        apply("dark");
        return "dark";
      }
    } catch (_) {}
    apply("light");
    return "light";
  }

  function toggle() {
    var next = current() === "dark" ? "light" : "dark";
    apply(next);
    persist(next);
    syncButtons(next);
    return next;
  }

  function syncButtons(theme) {
    document.querySelectorAll("[data-theme-toggle]").forEach(function (btn) {
      var dark = theme === "dark";
      btn.setAttribute("aria-pressed", dark ? "true" : "false");
      btn.setAttribute("title", dark ? "Switch to light canvas" : "Switch to dark canvas");
      var label = btn.querySelector("[data-theme-label]");
      if (label) label.textContent = dark ? "Light" : "Dark";
    });
  }

  window.ContextVerseTheme = { load: load, toggle: toggle, current: current, apply: apply };

  load();

  document.addEventListener("DOMContentLoaded", function () {
    syncButtons(current());
    document.querySelectorAll("[data-theme-toggle]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        toggle();
      });
    });

    var navBtn = document.querySelector("[data-nav-toggle]");
    if (navBtn) {
      navBtn.addEventListener("click", function () {
        document.body.classList.toggle("nav-open");
      });
    }
    document.body.addEventListener("click", function (e) {
      if (!document.body.classList.contains("nav-open")) return;
      if (e.target.closest(".app-sidebar") || e.target.closest("[data-nav-toggle]")) return;
      document.body.classList.remove("nav-open");
    });

    document.addEventListener("keydown", function (e) {
      if (e.key === "Escape") document.body.classList.remove("nav-open");
      if ((e.key === "d" || e.key === "D") && !e.metaKey && !e.ctrlKey && !e.altKey) {
        var t = e.target;
        if (t && (t.tagName === "INPUT" || t.tagName === "TEXTAREA" || t.tagName === "SELECT" || t.isContentEditable)) {
          return;
        }
        toggle();
      }
    });
  });
})();
