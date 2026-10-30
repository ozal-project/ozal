<script lang="ts">
  // How Ozal's sign-in layer works, for someone who has never put a login in
  // front of an app: four steps in order, then the details a developer needs.
  // The header names are the ones the proxy injects (internal/pipeline).
  let { host = "" }: { host?: string } = $props();
  const steps = $derived([
    { title: "A visitor opens", detail: host || "your app's address" , mono: !!host },
    { title: "Ozal asks them to sign in", detail: "With a passkey: fingerprint, face or PIN. No passwords." },
    { title: "Ozal checks your rule", detail: "Only the people you choose below get through. Others see a “no access” page." },
    { title: "Your app receives them", detail: "Already signed in, with their email. The app needs no login of its own." },
  ]);
</script>

<section class="bg-muted/40 rounded-lg border p-4" aria-labelledby="signin-flow-title">
  <h3 id="signin-flow-title" class="text-sm font-medium">How private access works</h3>
  <ol class="mt-3 grid gap-3 sm:grid-cols-4 sm:gap-2">
    {#each steps as step, i (step.title)}
      <li class="flex gap-2.5 sm:flex-col sm:gap-1.5">
        <span class="bg-background text-muted-foreground grid size-6 shrink-0 place-items-center rounded-full border text-xs tabular-nums" aria-hidden="true">{i + 1}</span>
        <span class="min-w-0">
          <span class="block text-xs font-medium">{step.title}</span>
          <span class={["text-muted-foreground block text-xs leading-snug break-words", step.mono && "font-mono"]}>{step.detail}</span>
        </span>
      </li>
    {/each}
  </ol>
  <details class="mt-3 text-xs">
    <summary class="text-muted-foreground hover:text-foreground cursor-pointer">For developers: what the app receives</summary>
    <div class="text-muted-foreground mt-2 grid gap-1.5 leading-relaxed">
      <p>Every request that reaches the app carries the signed-in person:</p>
      <p><code class="text-foreground font-mono">X-Auth-Request-Email</code> their email · <code class="text-foreground font-mono">X-Auth-Request-User</code> a stable ID for the account</p>
      <p>Ozal removes these headers from what visitors send, so they cannot be faked, and never forwards its session cookie. Use them to show who is signed in or to link records to a person. An app with its own login keeps it: Ozal decides who reaches the app, not what they can do inside.</p>
    </div>
  </details>
</section>
