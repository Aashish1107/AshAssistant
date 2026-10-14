<script lang="ts">
  // Browse to a folder: for "Move to…" and for files shared from other apps.
  import { api, messageOf, type Listing } from "../lib/api";
  import { crumbs } from "../lib/paths";
  import Icon from "./Icon.svelte";

  let { folder = $bindable("/"), hide = "" }: { folder?: string; /** A folder not to offer (the one being moved). */ hide?: string } = $props();

  let folders = $state<{ name: string; path: string }[]>([]);
  let loading = $state(true);
  let error = $state("");

  $effect(() => {
    const path = folder;
    loading = true;
    error = "";
    api<Listing>("GET", "/api/v1/files?path=" + encodeURIComponent(path))
      .then((listing) => {
        if (folder !== path) return;
        folders = listing.entries.filter((e) => e.type === "folder" && e.path !== hide);
      })
      .catch((problem) => {
        if (folder === path) error = messageOf(problem);
      })
      .finally(() => {
        if (folder === path) loading = false;
      });
  });
</script>

<nav class="crumbs" aria-label="Folder">
  {#each crumbs(folder) as crumb, i (crumb.path)}
    {#if i > 0}<Icon name="chevron" size={16} />{/if}
    <button class="crumb" aria-current={crumb.path === folder ? "location" : undefined} onclick={() => (folder = crumb.path)}>{crumb.name}</button>
  {/each}
</nav>
<ul class="folders" aria-busy={loading}>
  {#if error}
    <li class="note error-text">{error}</li>
  {:else}
    {#each folders as item (item.path)}
      <li><button class="folder" onclick={() => (folder = item.path)}><span class="tile kind-folder"><Icon name="folder" size={20} /></span>{item.name}</button></li>
    {:else}
      <li class="note muted">{loading ? "Loading…" : "No folders here"}</li>
    {/each}
  {/if}
</ul>

<style>
  .crumbs {
    display: flex;
    align-items: center;
    flex-wrap: wrap;
    gap: 2px;
    padding: 0 20px 8px;
    color: var(--muted);
  }
  .crumb {
    border: 0;
    background: none;
    padding: 6px 4px;
    color: var(--accent);
    font-weight: 600;
  }
  .crumb[aria-current] { color: var(--fg); }
  .folders {
    list-style: none;
    margin: 0;
    padding: 0;
    max-height: 45dvh;
    overflow-y: auto;
    border-top: 1px solid var(--border);
    border-bottom: 1px solid var(--border);
  }
  .folder {
    display: flex;
    align-items: center;
    gap: 14px;
    width: 100%;
    min-height: 56px;
    padding: 0 20px;
    border: 0;
    background: none;
    text-align: left;
  }
  .folder:hover { background: var(--hover); }
  .folder .tile { width: 34px; height: 34px; }
  .note { padding: 16px 20px; }
</style>
