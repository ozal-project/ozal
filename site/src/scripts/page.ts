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
  document.querySelectorAll(".hero-copy > *, .section-intro > *, .capabilities article, .map, .how > *").forEach((node) => observer.observe(node));
}
