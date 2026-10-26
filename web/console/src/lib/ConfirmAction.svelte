<script lang="ts">
  import { Button } from "$lib/components/ui/button";

  const dialogId = $props.id();
  type Props = {
    open?: boolean;
    title: string;
    target: string;
    current: string;
    requested: string;
    consequence: string;
    detail?: string;
    confirmLabel: string;
    onConfirm: () => void;
  };

  let { open = $bindable(false), title, target, current, requested, consequence, detail = "", confirmLabel, onConfirm }: Props = $props();
  let dialog = $state<HTMLDialogElement | null>(null);
  let cancelButton = $state<HTMLButtonElement | null>(null);

  $effect(() => {
    if (!dialog) return;
    if (open && !dialog.open) {
      dialog.showModal();
      requestAnimationFrame(() => cancelButton?.focus());
    } else if (!open && dialog.open) {
      dialog.close();
    }
  });

  function close() {
    open = false;
  }

  function confirm() {
    open = false;
    onConfirm();
  }
</script>

<dialog bind:this={dialog} aria-labelledby={`${dialogId}-title`} aria-describedby={`${dialogId}-description`}
  class="bg-card text-card-foreground border-border m-auto w-[min(34rem,calc(100vw-2rem))] rounded-xl border p-0 backdrop:bg-black/60"
  onclose={() => (open = false)} oncancel={close}>
  <div class="space-y-5 p-5 sm:p-6">
    <div>
      <p class="plate mb-2">Review change</p>
      <h2 id={`${dialogId}-title`} class="text-lg font-semibold">{title}</h2>
    </div>
    <dl id={`${dialogId}-description`} class="border-border divide-border divide-y rounded-lg border text-sm">
      <div class="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt class="text-muted-foreground">Target</dt><dd class="font-mono break-all">{target}</dd></div>
      <div class="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt class="text-muted-foreground">Current</dt><dd>{current}</dd></div>
      <div class="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt class="text-muted-foreground">Requested</dt><dd>{requested}</dd></div>
      <div class="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt class="text-muted-foreground">Consequence</dt><dd>{consequence}</dd></div>
      {#if detail}<div class="grid gap-1 px-3 py-2 sm:grid-cols-[7rem_1fr]"><dt class="text-muted-foreground">Details</dt><dd class="font-mono break-all">{detail}</dd></div>{/if}
    </dl>
    <div class="flex flex-wrap justify-end gap-2">
      <Button bind:ref={cancelButton} variant="outline" onclick={close}>Cancel</Button>
      <Button class="bg-destructive text-destructive-foreground hover:bg-destructive/90" onclick={confirm}>{confirmLabel}</Button>
    </div>
  </div>
</dialog>
