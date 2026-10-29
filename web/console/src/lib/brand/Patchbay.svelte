<script lang="ts">
  // The sign-in backdrop: a patch bay of jacks where a few cords connect and
  // carry a signal between resources.
  const cols = 14, rows = 8, gap = 72;
  const jacks = Array.from({ length: cols * rows }, (_, i) => ({ x: (i % cols) * gap + 36, y: Math.floor(i / cols) * gap + 36 }));
  const at = (c: number, r: number) => ({ x: c * gap + 36, y: r * gap + 36 });
  const cord = (a: [number, number], b: [number, number]) => {
    const p = at(...a), q = at(...b);
    const sag = Math.abs(q.x - p.x) * 0.35 + 40;
    return `M${p.x} ${p.y} C${p.x} ${p.y + sag}, ${q.x} ${q.y + sag}, ${q.x} ${q.y}`;
  };
  const cords = [cord([1, 1], [5, 3]), cord([3, 6], [8, 4]), cord([9, 1], [12, 5]), cord([6, 0], [10, 2]), cord([11, 6], [13, 3])];
  const lit = [at(1, 1), at(5, 3), at(3, 6), at(8, 4), at(9, 1), at(12, 5), at(6, 0), at(10, 2), at(11, 6), at(13, 3)];
</script>

<svg class="patchbay" viewBox="0 0 1008 576" preserveAspectRatio="xMidYMid slice" aria-hidden="true">
  {#each jacks as j, i (i)}<circle cx={j.x} cy={j.y} r="2.2" class="patch-jack" />{/each}
  {#each cords as d, i (i)}
    <path {d} class={`patch-cord patch-cord-${i}`} pathLength="1" />
    <path {d} class={`patch-signal patch-cord-${i}`} pathLength="1" />
  {/each}
  {#each lit as j, i (i)}<circle cx={j.x} cy={j.y} r="4.5" class={`patch-lit patch-cord-${Math.floor(i / 2)}`} />{/each}
</svg>
