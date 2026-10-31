<script lang="ts">
  import { Button } from "$lib/components/ui/button";
  import { choices } from "$lib/passkeyChoices";

  // The passkey somewhere else: the same sign-in, steered to that place.
  // Opened by the caller after a prompt that ended without a passkey.
  let { open = false, disabled = false, onuse }: { open?: boolean; disabled?: boolean; onuse: (hints: string[]) => void } = $props();
</script>

<details class="text-muted-foreground mt-4 text-xs" {open}>
  <summary class="hover:text-foreground cursor-pointer">Passkey somewhere else?</summary>
  <p class="mt-2 leading-relaxed">A password manager's browser extension (Bitwarden or self-hosted Vaultwarden, 1Password, Proton Pass, KeePassXC or another) takes the prompt by itself.</p>
  <div class="mt-3 grid gap-2">
    <Button variant="outline" size="sm" {disabled} onclick={() => onuse(choices.phone.hints)}>Use my phone</Button>
    <Button variant="outline" size="sm" {disabled} onclick={() => onuse(choices.key.hints)}>Use a security key</Button>
  </div>
</details>
