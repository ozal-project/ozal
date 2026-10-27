<script lang="ts">
  import type { Snippet } from "svelte";
  import XIcon from "@lucide/svelte/icons/x";

  // A task that needs a form opens here instead of sitting on the page.
  // Escape and the close button cancel; focus returns to what opened it.
  let { open = $bindable(false), title, intro = "", wide = false, children }: { open?: boolean; title: string; intro?: string; wide?: boolean; children: Snippet } = $props();
  const id = $props.id();
  let dialog = $state<HTMLDialogElement | null>(null);
  let returnFocus: HTMLElement | null = null;

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) {
      returnFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
      dialog.showModal();
      requestAnimationFrame(() => dialog?.querySelector<HTMLElement>("input:not([type=hidden]):not(:disabled), select, textarea, button:not([data-close])")?.focus());
    } else if (!open && dialog.open) {
      dialog.close();
    }
  });

  function closed() {
    open = false;
    requestAnimationFrame(() => { if (returnFocus?.isConnected) returnFocus.focus(); returnFocus = null; });
  }

  function containTab(event: KeyboardEvent) {
    if (event.key !== "Tab" || !dialog) return;
    const focusable = [...dialog.querySelectorAll<HTMLElement>('button:not(:disabled), a[href], input:not(:disabled), select:not(:disabled), textarea:not(:disabled), [tabindex]:not([tabindex="-1"])')]
      .filter((element) => element.getClientRects().length > 0);
    if (!focusable.length) return;
    const first = focusable[0], last = focusable[focusable.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
</script>

<dialog bind:this={dialog} aria-labelledby={`${id}-title`} onclose={closed} onkeydown={containTab}
  class={["bg-card text-card-foreground border-border m-auto max-h-[calc(100dvh-2rem)] rounded-xl border p-0 backdrop:bg-black/60", wide ? "w-[min(44rem,calc(100vw-2rem))]" : "w-[min(30rem,calc(100vw-2rem))]"]}>
  {#if open}
    <div class="p-5 sm:p-6">
      <div class="mb-5 flex items-start justify-between gap-4">
        <div>
          <h2 id={`${id}-title`} class="text-lg font-semibold">{title}</h2>
          {#if intro}<p class="text-muted-foreground mt-1 text-sm">{intro}</p>{/if}
        </div>
        <button type="button" data-close class="text-muted-foreground hover:text-foreground hover:bg-accent -mt-1 -mr-1 rounded-md p-1.5" aria-label="Close" onclick={() => (open = false)}><XIcon class="size-4" /></button>
      </div>
      {@render children()}
    </div>
  {/if}
</dialog>
