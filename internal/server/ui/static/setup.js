(function () {
  function ready(fn) {
    if (document.readyState !== "loading") fn();
    else document.addEventListener("DOMContentLoaded", fn);
  }

  ready(function () {
    var form = document.getElementById("setup-form");
    if (!form) return;

    var panes = Array.prototype.slice.call(form.querySelectorAll(".wizard-pane"));
    var indicators = Array.prototype.slice.call(document.querySelectorAll("[data-step-indicator]"));
    var step = parseInt(form.getAttribute("data-start-step") || "1", 10);
    if (isNaN(step) || step < 1) step = 1;
    if (step > panes.length) step = panes.length;

    var backendSelect = form.querySelector("[data-backend-select]");

    function showBackendPanels() {
      var driver = backendSelect ? backendSelect.value : "local";
      form.querySelectorAll("[data-backend-panel]").forEach(function (el) {
        var on = el.getAttribute("data-backend-panel") === driver;
        el.hidden = !on;
      });
    }

    function validatePane(pane) {
      var fields = pane.querySelectorAll("input, select, textarea");
      for (var i = 0; i < fields.length; i++) {
        var el = fields[i];
        if (el.disabled || el.hidden || el.closest("[hidden]")) continue;
        if (typeof el.checkValidity === "function" && !el.checkValidity()) {
          el.reportValidity();
          return false;
        }
      }
      return true;
    }

    function fillReview() {
      function val(name) {
        var el = form.elements.namedItem(name);
        return el && "value" in el ? String(el.value || "") : "";
      }
      form.querySelectorAll("[data-review]").forEach(function (el) {
        var key = el.getAttribute("data-review");
        if (key === "password_set") {
          el.textContent = val("password") ? "set (userpass enabled)" : "not set (token login only)";
          return;
        }
        el.textContent = val(key) || "—";
      });
    }

    function go(n) {
      if (n < 1 || n > panes.length) return;
      step = n;
      panes.forEach(function (pane) {
        var s = parseInt(pane.getAttribute("data-step"), 10);
        var on = s === step;
        pane.classList.toggle("is-active", on);
        pane.hidden = !on;
      });
      indicators.forEach(function (li) {
        var s = parseInt(li.getAttribute("data-step-indicator"), 10);
        li.classList.toggle("is-active", s === step);
        li.classList.toggle("is-done", s < step);
      });
      if (step === 4) fillReview();
      var focusable = form.querySelector('.wizard-pane.is-active input, .wizard-pane.is-active select');
      if (focusable) focusable.focus();
    }

    form.querySelectorAll("[data-next]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        var pane = form.querySelector(".wizard-pane.is-active");
        if (!validatePane(pane)) return;
        go(step + 1);
      });
    });
    form.querySelectorAll("[data-prev]").forEach(function (btn) {
      btn.addEventListener("click", function () {
        go(step - 1);
      });
    });

    if (backendSelect) {
      backendSelect.addEventListener("change", showBackendPanels);
      showBackendPanels();
    }

    form.addEventListener("submit", function (e) {
      for (var i = 0; i < panes.length; i++) {
        var pane = panes[i];
        var fields = pane.querySelectorAll("input, select, textarea");
        for (var j = 0; j < fields.length; j++) {
          var el = fields[j];
          if (el.disabled) continue;
          if (el.closest("[data-backend-panel][hidden]")) continue;
          if (!el.required) continue;
          if (!String(el.value || "").trim()) {
            e.preventDefault();
            go(i + 1);
            if (typeof el.reportValidity === "function") el.reportValidity();
            else el.focus();
            return;
          }
        }
      }
    });

    go(step);
  });
})();
