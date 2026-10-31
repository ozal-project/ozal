<script lang="ts">
  import { onMount } from "svelte";
  import { choices, passkeyChoices, readCapabilities, recommendedChoice, type Capabilities, type Choice } from "$lib/passkeyChoices";

  // Where the new passkey will live, asked before the prompt and passed to the
  // browser as a hint. Until the person picks, a better-informed answer from
  // the browser may move the default; after that it never does.
  let { choice = $bindable<Choice>("device") }: { choice?: Choice } = $props();
  let caps = $state<Capabilities>(null);
  let picked = false;
  const order = $derived(passkeyChoices(caps));
  const recommended = $derived(recommendedChoice(caps));
  onMount(() => {
    void readCapabilities(window.PublicKeyCredential as never).then((got) => {
      caps = got;
      if (!picked) choice = passkeyChoices(got)[0];
    });
  });
</script>

<fieldset class="grid gap-2">
  <legend class="mb-2 text-sm font-medium">Where will you keep this passkey?</legend>
  {#each order as option (option)}
    <label class="border-input has-[:checked]:border-foreground grid cursor-pointer grid-cols-[auto_1fr] gap-x-2.5 rounded-md border px-3 py-2 text-xs">
      <input type="radio" name="keep" value={option} bind:group={choice} onchange={() => { picked = true; }} class="row-span-2 mt-0.5 accent-[var(--primary)]" />
      <span class="text-foreground text-sm font-medium">{choices[option].label}{#if option === recommended}<span class="text-muted-foreground ml-1 font-normal">· recommended here</span>{/if}</span>
      <span class="text-muted-foreground leading-relaxed">{choices[option].detail}</span>
    </label>
  {/each}
</fieldset>
