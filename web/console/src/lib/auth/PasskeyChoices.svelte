<script lang="ts">
  import { onMount } from "svelte";
  import { choiceText, passkeyChoices, readCapabilities } from "$lib/passkeyChoices";

  // Opened by the caller after a prompt that ended without a passkey: the
  // browser does not say why, so the console shows what else can work.
  let { open = $bindable(false) }: { open?: boolean } = $props();
  let choices = $state(passkeyChoices(null));
  onMount(() => {
    void readCapabilities(window.PublicKeyCredential as never).then((caps) => { choices = passkeyChoices(caps); });
  });
</script>

<details class="text-muted-foreground mt-4 text-xs" bind:open>
  <summary class="hover:text-foreground cursor-pointer">What can hold a passkey?</summary>
  <ul class="mt-2 grid list-disc gap-1 pl-4 leading-relaxed">
    {#each choices as choice (choice)}<li>{choiceText[choice]}</li>{/each}
  </ul>
</details>
