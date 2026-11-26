(() => {
  "use strict";

  const $ = (id) => document.getElementById(id);
  const el = (tag, cls, attrs = {}) => {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    for (const [k, v] of Object.entries(attrs)) n.setAttribute(k, v);
    return n;
  };

  let statusTimer = null;

  // ── Banner ────────────────────────────────────────────────────────────
  function banner(kind, title, text) {
    const b = $("banner");
    b.className = "banner " + kind;
    b.replaceChildren();
    const s = el("strong");
    s.textContent = title;
    b.append(s, document.createTextNode(text));
    b.hidden = false;
    b.onclick = () => (b.hidden = true);
  }

  // ── Wallpaper ─────────────────────────────────────────────────────────
  function setWallpaper(wp) {
    const w = $("wallpaper");
    if (wp && wp.type === "image") {
      w.removeAttribute("data-preset");
      w.setAttribute("data-image", "");
      w.style.backgroundImage = `url("${wp.value.replace(/"/g, "%22")}")`;
    } else {
      w.removeAttribute("data-image");
      w.style.backgroundImage = "";
      w.dataset.preset = (wp && wp.value) || "aurora";
    }
  }

  // ── Clock ─────────────────────────────────────────────────────────────
  function startClock() {
    $("clock").hidden = false;
    const dateFmt = new Intl.DateTimeFormat(undefined, { weekday: "long", month: "long", day: "numeric" });
    const timeFmt = new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" });
    const tick = () => {
      const now = new Date();
      $("clock-date").textContent = dateFmt.format(now);
      // Drop AM/PM for the big iOS-style numerals.
      $("clock-time").textContent = timeFmt.format(now).replace(/\s?[AP]\.?M\.?$/i, "");
    };
    tick();
    setInterval(tick, 1000);
  }

  // ── Apps ──────────────────────────────────────────────────────────────
  function appNode(app, i) {
    const a = el("a", "app", { href: app.url || "#", title: app.name, "data-id": app.id });
    a.style.setProperty("--i", i);
    if (app.target) {
      a.target = app.target;
      a.rel = "noopener noreferrer";
    }

    const wrap = el("span", "icon-wrap");
    const icon = el("span", "icon");
    const img = el("img", "", { alt: "", draggable: "false", decoding: "async" });
    img.src = app.icon;
    img.onerror = () => {
      // External icon URL failed → fall back to the server's naming-convention lookup.
      img.onerror = null;
      if (img.getAttribute("src") !== app.fallback) img.src = app.fallback;
    };
    icon.append(img);

    const badge = el("span", "badge", { "aria-hidden": "true" });
    badge.textContent = "!";
    wrap.append(icon, badge);

    const label = el("span", "label");
    label.textContent = app.name;

    a.append(wrap, label);
    return a;
  }

  function renderPages(pages) {
    const container = $("pages");
    const dots = $("dots");
    container.replaceChildren();
    dots.replaceChildren();

    if (!pages.length) {
      const p = el("section", "page");
      const e = el("div", "empty");
      e.textContent = "No apps configured yet — add some to config.yaml.";
      p.append(e);
      container.append(p);
    }

    let i = 0;
    pages.forEach((page, idx) => {
      const section = el("section", "page", { "aria-label": page.name || `Page ${idx + 1}` });
      const grid = el("div", "grid");
      page.apps.forEach((app) => grid.append(appNode(app, i++)));
      section.append(grid);
      container.append(section);

      const dot = el("button", "dot", { type: "button", "aria-label": page.name || `Page ${idx + 1}`, title: page.name || "" });
      dot.onclick = () => goTo(idx);
      dots.append(dot);
    });

    dots.hidden = pages.length < 2;
    updateDots();
  }

  function renderDock(apps) {
    const dock = $("dock");
    dock.replaceChildren();
    apps.forEach((app, i) => dock.append(appNode(app, i)));
    dock.hidden = apps.length === 0;
  }

  // ── Paging ────────────────────────────────────────────────────────────
  const currentPage = () => {
    const c = $("pages");
    return c.clientWidth ? Math.round(c.scrollLeft / c.clientWidth) : 0;
  };

  function goTo(idx) {
    const c = $("pages");
    const max = c.children.length - 1;
    idx = Math.max(0, Math.min(max, idx));
    c.scrollTo({ left: idx * c.clientWidth });
  }

  function updateDots() {
    const cur = currentPage();
    [...$("dots").children].forEach((d, i) => d.classList.toggle("active", i === cur));
  }

  $("pages").addEventListener("scroll", () => requestAnimationFrame(updateDots), { passive: true });
  window.addEventListener("resize", updateDots);
  window.addEventListener("keydown", (e) => {
    if (e.target.closest && e.target.closest("input, textarea")) return;
    if (e.key === "ArrowRight") goTo(currentPage() + 1);
    if (e.key === "ArrowLeft") goTo(currentPage() - 1);
  });

  // Mouse-wheel (vertical) → horizontal paging on desktop, one page per gesture.
  let wheelLock = 0;
  $("pages").addEventListener("wheel", (e) => {
    const c = $("pages");
    if (c.children.length < 2 || Math.abs(e.deltaX) > Math.abs(e.deltaY)) return;
    const page = c.children[currentPage()];
    if (page && page.scrollHeight > page.clientHeight) return; // let tall pages scroll
    e.preventDefault();
    const now = Date.now();
    if (now - wheelLock < 450 || Math.abs(e.deltaY) < 8) return;
    wheelLock = now;
    goTo(currentPage() + (e.deltaY > 0 ? 1 : -1));
  }, { passive: false });

  // ── Status ────────────────────────────────────────────────────────────
  async function refreshStatus() {
    if (document.hidden) return;
    try {
      const res = await fetch("api/status", { cache: "no-store" });
      const status = await res.json();
      if (status.error) return;
      document.querySelectorAll(".app[data-id]").forEach((a) => {
        const up = status[a.dataset.id];
        a.classList.toggle("down", up === false);
        a.title = up === false ? `${a.querySelector(".label").textContent} — unreachable` : a.querySelector(".label").textContent;
      });
    } catch {
      /* dashboard server unreachable — leave badges as they are */
    }
  }

  // ── Boot ──────────────────────────────────────────────────────────────
  async function load() {
    let cfg;
    try {
      const res = await fetch("api/config", { cache: "no-store" });
      cfg = await res.json();
    } catch (err) {
      banner("error", "Could not load config", String(err));
      return;
    }

    if (cfg.error) {
      banner("error", "Config error", cfg.error);
      return;
    }
    if (cfg.warning) banner("info", "Heads up", cfg.warning);

    document.title = cfg.title;
    const appleTitle = document.querySelector('meta[name="apple-mobile-web-app-title"]');
    if (appleTitle) appleTitle.content = cfg.title;

    if (cfg.columns > 0) document.documentElement.style.setProperty("--cols-wide", cfg.columns);
    setWallpaper(cfg.wallpaper);
    if (cfg.clock) startClock();

    renderPages(cfg.pages || []);
    renderDock(cfg.dock || []);

    if (cfg.statusChecks) {
      refreshStatus();
      statusTimer = setInterval(refreshStatus, (cfg.checkInterval || 30) * 1000);
      document.addEventListener("visibilitychange", () => !document.hidden && refreshStatus());
    }
  }

  load();
})();
