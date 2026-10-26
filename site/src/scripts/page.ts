// Content is visible by default. Animate an arrival only after it intersects:
// a blocked script or a missing observer can never leave the page hidden.
if (!matchMedia("(prefers-reduced-motion: reduce)").matches && "IntersectionObserver" in window) {
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      entry.target.classList.add("motion-enter");
      observer.unobserve(entry.target);
    }
  }, { threshold: 0.1 });
  document.querySelectorAll(".hero-copy > *, .section-intro > *, .capabilities article, .product-copy, .product-shot").forEach((node) => observer.observe(node));
}

// The tour is a scroller: the step in view stays pinned and the others dim.
// Without JavaScript every step is fully visible, so only mark it live here.
const tour = document.querySelector<HTMLElement>("[data-tour]");
if (tour && "IntersectionObserver" in window) {
  const steps = [...tour.querySelectorAll<HTMLElement>(".step")];
  const links = [...document.querySelectorAll<HTMLAnchorElement>(".tour-rail a")];
  const show = (index: number) => {
    steps.forEach((step, i) => step.classList.toggle("is-active", i === index));
    links.forEach((link, i) => (i === index ? link.setAttribute("aria-current", "step") : link.removeAttribute("aria-current")));
  };
  show(0);
  tour.setAttribute("data-live", "");
  const seen = new Set<number>([0]);
  const watcher = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      const index = steps.indexOf(entry.target as HTMLElement);
      if (entry.isIntersecting) seen.add(index); else seen.delete(index);
    }
    if (seen.size) show(Math.min(...seen));
  }, { rootMargin: "-35% 0px -45% 0px" });
  steps.forEach((step) => watcher.observe(step));
}
