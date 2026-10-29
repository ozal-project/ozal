// Content is visible by default. Animate an arrival only after it intersects:
// a blocked script or a missing observer can never leave the page hidden.
// The first screen animates with CSS alone, so it is not observed here.
if (!matchMedia("(prefers-reduced-motion: reduce)").matches && "IntersectionObserver" in window) {
  const observer = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (!entry.isIntersecting) continue;
      entry.target.classList.add("motion-enter");
      observer.unobserve(entry.target);
    }
  }, { threshold: 0.1 });
  document.querySelectorAll<HTMLElement>(".try-copy > *, .section-intro > *, .works li, .timeline li, .map, .how > *, .walk-tabs, .availability > *").forEach((node) => {
    // Siblings arrive one after another instead of as a block.
    const i = node.parentElement ? [...node.parentElement.children].indexOf(node) : 0;
    node.style.setProperty("--i", String(Math.min(i, 8)));
    observer.observe(node);
  });

  // The pipeline loops only while on screen; the trace types itself once.
  const play = new IntersectionObserver((entries) => {
    for (const entry of entries) {
      if (entry.target.classList.contains("terminal")) {
        if (entry.isIntersecting) { entry.target.classList.add("is-playing"); play.unobserve(entry.target); }
      } else entry.target.classList.toggle("is-playing", entry.isIntersecting);
    }
  }, { threshold: 0.3 });
  document.querySelectorAll(".pipeline-strip, .terminal").forEach((node) => play.observe(node));
}
