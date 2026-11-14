// Content is visible by default. Animate an arrival only after it intersects:
// a blocked script or a missing observer can never leave the page hidden.
// The first screen animates with CSS alone, so it is not observed here.
const still = matchMedia("(prefers-reduced-motion: reduce)").matches;
const observing = "IntersectionObserver" in window;

if (!still && observing) {
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      entry.target.classList.add("motion-enter");
      observer.unobserve(entry.target);
    }
  }, { threshold: 0.1 });
  document.querySelectorAll<HTMLElement>(".head > *, .options > li, .pipeline > li, .how-side > *, .more > *, .works > li, .fit > *, .start > *").forEach((node) => {
    // Siblings arrive one after another instead of as a block.
    const i = node.parentElement ? [...node.parentElement.children].indexOf(node) : 0;
    node.style.setProperty("--i", String(Math.min(i, 8)));
    observer.observe(node);
  });

  // The pipeline loops only while on screen; the trace types itself once.
  const play = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.target.hasAttribute("data-type")) {
        if (entry.isIntersecting) { entry.target.classList.add("is-playing"); play.unobserve(entry.target); }
      } else entry.target.classList.toggle("is-playing", entry.isIntersecting);
    }
  }, { threshold: 0.3 });
  document.querySelectorAll(".pipeline, [data-type]").forEach((node) => play.observe(node));
}

// Mark the nav link of the section being read. Plain state, not motion.
const links = [...document.querySelectorAll<HTMLAnchorElement>('header nav a[href^="/#"]')];
if (links.length && observing) {
  const byId = new Map(links.map((a) => [a.getAttribute("href")!.slice(2), a]));
  // Sections without a nav link still count, so a link is not left lit while reading the next section.
  const sections = ["how", "engine", "tour", "status", "start"];
  const spy = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      for (const a of links) a.removeAttribute("aria-current");
      byId.get(entry.target.id)?.setAttribute("aria-current", "location");
    }
  }, { rootMargin: "-35% 0px -60% 0px" });
  for (const id of sections) { const section = document.getElementById(id); if (section) spy.observe(section); }
}

// The doorway plays its three requests in turn while it is on screen, twice,
// then rests on the first. The first click or key inside hands it to the
// visitor for good. Reduced motion: it stays on the first request.
const door = document.querySelector<HTMLElement>("[data-door]");
if (door && !still && observing) {
  const scenes = [...door.querySelectorAll<HTMLInputElement>('input[name="scene"]')];
  const hold = 5200;
  let shown = 0;
  let timer = 0;
  const next = () => {
    shown++;
    scenes[shown % scenes.length].checked = true;
    if (shown >= scenes.length * 2) stop();
  };
  const seen = new IntersectionObserver((entries) => {
    clearInterval(timer);
    if (entries.some((e) => e.isIntersecting)) timer = window.setInterval(next, hold);
  }, { threshold: 0.5 });
  const stop = () => { clearInterval(timer); seen.disconnect(); door.classList.add("door-chosen"); };
  door.addEventListener("pointerdown", stop, { once: true });
  door.addEventListener("keydown", stop, { once: true });
  seen.observe(door);
}

// The tour: on a wide screen the five screens share one sticky stage, and the
// step whose text crosses the middle of the viewport is the one shown. The
// page scrolls normally; without this script every step keeps its own screen.
const walk = document.querySelector<HTMLElement>("[data-walk]");
if (walk && observing) {
  const steps = [...walk.querySelectorAll<HTMLElement>(".step")];
  walk.classList.add("is-scrolly");
  const stage = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      const active = entry.target.closest(".step");
      for (const step of steps) step.classList.toggle("is-active", step === active);
    }
  }, { rootMargin: "-50% 0px -50% 0px" });
  for (const step of steps) { const copy = step.querySelector(".step-copy"); if (copy) stage.observe(copy); }
}

// Copy buttons appear only when the clipboard can be written.
for (const button of document.querySelectorAll<HTMLButtonElement>("[data-copy]")) {
  const source = button.closest("figure")?.querySelector("[data-copy-source]");
  if (!source || !navigator.clipboard) continue;
  button.hidden = false;
  button.addEventListener("click", async () => {
    try {
      await navigator.clipboard.writeText(source.textContent ?? "");
      button.textContent = "Copied";
    } catch {
      button.textContent = "Select and copy";
    }
    setTimeout(() => (button.textContent = "Copy"), 2000);
  });
}
