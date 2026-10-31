<script lang="ts">
  import { onMount } from "svelte";
  import { choices, goodFor, passkeyChoices, readCapabilities, recommendedChoice, type Capabilities, type Choice } from "$lib/passkeyChoices";

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
  <details class="text-muted-foreground text-xs">
    <summary class="hover:text-foreground cursor-pointer">Not sure which to choose?</summary>
    <dl class="mt-2 grid gap-1.5 leading-relaxed">
      {#each order as option (option)}<div><dt class="text-foreground inline font-medium">{choices[option].label}:</dt> <dd class="inline">{goodFor[option]}</dd></div>{/each}
    </dl>
    <p class="mt-2 leading-relaxed">Add a second passkey in another place later from your account menu, so losing one does not lock you out.</p>
  </details>
</fieldset>
