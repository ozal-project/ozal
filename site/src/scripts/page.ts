// The page's motion layer. Static content is already finished; this only
// brings parts in once as they arrive, opens the hero picture, rolls the
// figures and stills the loop for people who ask for less motion.
const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;

// Parts arrive in reading order, once. Without IntersectionObserver or with
// reduced motion everything is simply shown.
const arrivals = [...document.querySelectorAll<HTMLElement>("[data-reveal], .shutter, .bar")];
if (reduced || !("IntersectionObserver" in window)) {
  arrivals.forEach((el) => el.classList.add("is-in"));
} else {
  const io = new IntersectionObserver((entries) => {
    for (const e of entries) {
      if (!e.isIntersecting) continue;
      e.target.classList.add("is-in");
      io.unobserve(e.target);
    }
  }, { rootMargin: "0px 0px -12% 0px", threshold: 0.08 });
  arrivals.forEach((el) => io.observe(el));
}

// The loop is decoration: it never plays for someone who asked for less motion.
document.querySelectorAll<HTMLVideoElement>("video[data-loop]").forEach((video) => {
  if (reduced) { video.pause(); video.removeAttribute("autoplay"); return; }
  // Off screen it costs battery for nothing.
  new IntersectionObserver((entries) => entries.forEach((e) => (e.isIntersecting ? void video.play().catch(() => {}) : video.pause()))).observe(video);
});

// Figures count up once as their tile arrives. The tile fades in at the same
// moment, so the number is never seen at its final value and then reset.
document.querySelectorAll<HTMLElement>("[data-count]").forEach((el) => {
  const final = el.textContent?.trim() ?? "";
  const target = Number(final);
  if (reduced || !Number.isInteger(target)) return;
  const run = () => {
    const start = performance.now();
    const tick = (now: number) => {
      const t = Math.min(1, (now - start) / 1100);
      el.textContent = t < 1 ? String(Math.round(target * (1 - Math.pow(1 - t, 4)))) : final;
      if (t < 1) requestAnimationFrame(tick);
    };
    requestAnimationFrame(tick);
  };
  new IntersectionObserver((entries, o) => entries.forEach((e) => { if (e.isIntersecting) { o.disconnect(); run(); } }), { rootMargin: "0px 0px -15% 0px" }).observe(el);
});

// The hero picture opens on load, without waiting for an observer.
requestAnimationFrame(() => document.querySelectorAll(".shutter").forEach((el) => el.classList.add("is-in")));
