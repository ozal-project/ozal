<script lang="ts">
  import { admin } from "$lib/adminToken.svelte";
  import { fetchGroups, type Group } from "$lib/api";
  import Panel from "$lib/Panel.svelte";
  let { refreshedAt }: { refreshedAt: number | null } = $props();
  let groups = $state<Group[] | null>(null);
  let error = $state("");
  $effect(() => {
    const token = admin.token.trim(); void refreshedAt;
    const controller = new AbortController();
    if (!token) { groups = null; error = ""; return; }
    void fetchGroups(token, controller.signal).then((result) => { if (!controller.signal.aborted) { groups = result; error = ""; } }).catch((e) => { if (!controller.signal.aborted) error = (e as Error).message; });
    return () => controller.abort();
  });
</script>
<Panel label="Access groups" title="Membership and application requirements" class="mb-6">
  <p class="text-muted-foreground mb-3 text-sm">Groups are created by assigning a name to a native person or requiring it on an application. Each required group must match. The provider owns OIDC memberships; this catalog does not import or edit them.</p>
  {#if error}<p class="text-destructive text-sm" role="status">{error} Refresh People to retry.</p>
  {:else if groups}<ul class="divide-y">{#each groups as group (group.name)}<li class="py-3"><h3 class="text-sm font-semibold">{group.name}</h3><p class="text-muted-foreground mt-1 break-all text-sm">Local members: {group.members.length ? group.members.join(", ") : "None"}</p><p class="mt-1 break-all text-sm">Required by: {group.hosts.length ? group.hosts.join(", ") : "No configured app"}</p></li>{:else}<li class="text-sm">No memberships or group requirements yet. Assign a group in an invitation or Edit groups, then require it in Applications.</li>{/each}</ul>
  {:else}<p class="text-muted-foreground text-sm">Unlock changes to read the group catalog.</p>{/if}
</Panel>
