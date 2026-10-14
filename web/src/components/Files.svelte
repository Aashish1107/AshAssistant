<script lang="ts">
  import { untrack } from "svelte";
  import { newFolder, remove, rename } from "../lib/actions";
  import type { Entry } from "../lib/api";
  import { folder, openFolder, refreshFolder } from "../lib/folders.svelte";
  import { baseName, contentURL, crumbs, formatDate, formatSize, kindOf, parentOf, viewable } from "../lib/paths";
  import { currentFolder, link } from "../lib/router.svelte";
  import { session } from "../lib/session.svelte";
  import { addUploads } from "../lib/uploads.svelte";
  import AccountSheet from "./AccountSheet.svelte";
  import Icon from "./Icon.svelte";
  import ItemSheet, { type ItemAction } from "./ItemSheet.svelte";
  import MoveDialog from "./MoveDialog.svelte";
  import NameDialog from "./NameDialog.svelte";
  import TypeIcon from "./TypeIcon.svelte";

  const path = $derived(currentFolder());
  const title = $derived(path === "/" ? "Ash Cloud" : baseName(path));

  let picker: HTMLInputElement;
  let menuFor = $state<Entry | null>(null);
  let renaming = $state<Entry | null>(null);
  let moving = $state<Entry | null>(null);
  let creating = $state(false);
  let account = $state(false);
  let dropping = $state(false);

  $effect(() => {
    const showing = path;
    untrack(() => openFolder(showing)); // depends on the path only
  });

  // Files may have changed on the laptop while the app was in the background.
  $effect(() => {
    const again = () => {
      if (document.visibilityState === "visible") refreshFolder();
    };
    document.addEventListener("visibilitychange", again);
    window.addEventListener("online", again);
    document.body.classList.add("has-fab");
    return () => {
      document.removeEventListener("visibilitychange", again);
      window.removeEventListener("online", again);
      document.body.classList.remove("has-fab");
    };
  });

  function openLink(entry: Entry): { href: string; external: boolean } {
    if (entry.type === "folder") return { href: link.folder(entry.path), external: false };
    if (kindOf(entry) === "video") return { href: link.player(entry.path), external: false };
    if (viewable(entry)) return { href: link.viewer(entry.path), external: false };
    return { href: contentURL(entry.path, true), external: true };
  }

  function act(entry: Entry, action: ItemAction) {
    if (action === "rename") renaming = entry;
    if (action === "move") moving = entry;
    if (action === "delete") remove(entry);
  }

  function picked(event: Event) {
    const input = event.currentTarget as HTMLInputElement;
    const files = [...(input.files ?? [])];
    input.value = ""; // so picking the same file again works
    if (files.length) addUploads(files, path);
  }

  function drop(event: DragEvent) {
    event.preventDefault();
    dropping = false;
    const files = [...(event.dataTransfer?.files ?? [])];
    if (files.length) addUploads(files, path);
  }
</script>

<!-- svelte-ignore a11y_no_static_element_interactions -->
<div
  class="files"
  class:dropping
  ondragover={(e) => {
    if (e.dataTransfer?.types.includes("Files")) {
      e.preventDefault();
      dropping = true;
    }
  }}
  ondragleave={() => (dropping = false)}
  ondrop={drop}
>
  <header class="top">
    {#if path === "/"}
      <span class="logo" aria-hidden="true"><Icon name="cloud" size={22} /></span>
    {:else}
      <a class="icon-button" href={link.folder(parentOf(path))} aria-label="Up to {parentOf(path) === '/' ? 'Home' : baseName(parentOf(path))}"><Icon name="back" /></a>
    {/if}
    <h1>{title}</h1>
    <button class="icon-button" aria-label="New folder" title="New folder" onclick={() => (creating = true)}><Icon name="folder-plus" /></button>
    <button class="icon-button avatar" aria-label="Account: {session.current?.user.username}" onclick={() => (account = true)}>
      {session.current?.user.username.slice(0, 1).toUpperCase()}
    </button>
  </header>

  {#if path !== "/"}
    <nav class="crumbs" aria-label="Folder">
      {#each crumbs(path) as crumb, i (crumb.path)}
        {#if i > 0}<Icon name="chevron" size={14} />{/if}
        {#if crumb.path === path}
          <span aria-current="location">{crumb.name}</span>
        {:else}
          <a href={link.folder(crumb.path)}>{crumb.name}</a>
        {/if}
      {/each}
    </nav>
  {/if}

  {#if folder.offline}
    <div class="banner" role="alert">
      <Icon name="wifi" size={20} />
      <span>Can't reach the laptop{folder.loaded ? " - showing what was here before" : ""}.</span>
      <button class="button quiet small" onclick={() => refreshFolder()}>Retry</button>
    </div>
  {:else if folder.error}
    <div class="banner" role="alert">
      <Icon name="alert" size={20} />
      <span>{folder.error}</span>
      {#if path !== "/"}<a class="button quiet small" href={link.folder("/")}>Home</a>{/if}
    </div>
  {/if}

  <main>
    {#if folder.loaded && folder.entries.length}
      <ul class="list" aria-label="Files in {title}" aria-busy={folder.loading}>
        {#each folder.entries as entry (entry.path)}
          {@const target = openLink(entry)}
          <li class="row">
            <a class="open" href={target.href} target={target.external ? "_blank" : undefined} rel={target.external ? "noopener" : undefined}>
              <TypeIcon kind={kindOf(entry)} />
              <span class="text">
                <span class="name">{entry.name}</span>
                <span class="meta">{entry.type === "folder" ? "Folder" : formatSize(entry.size ?? 0)} · {formatDate(entry.modified)}</span>
              </span>
            </a>
            <button class="icon-button more" aria-label="More for {entry.name}" onclick={() => (menuFor = entry)}><Icon name="more" /></button>
          </li>
        {/each}
      </ul>
    {:else if folder.loaded && !folder.error}
      <div class="empty">
        <span class="tile kind-folder big"><Icon name="folder" size={32} /></span>
        <p><strong>This folder is empty</strong></p>
        <p class="muted">Tap Upload to add photos, videos or any other files.</p>
      </div>
    {:else if !folder.error && !folder.offline}
      <p class="loading muted" aria-live="polite">Loading…</p>
    {/if}
  </main>

  <button class="fab" onclick={() => picker.click()}><Icon name="upload" /> Upload</button>
  <input bind:this={picker} type="file" multiple hidden onchange={picked} />
</div>

{#if menuFor}
  {@const entry = menuFor}
  <ItemSheet {entry} onaction={(action) => act(entry, action)} onclose={() => (menuFor = null)} />
{/if}
{#if renaming}
  {@const entry = renaming}
  <NameDialog title="Rename" value={entry.name} okLabel="Rename" selectStem={entry.type === "file"} onsubmit={(name) => rename(entry, name)} onclose={() => (renaming = null)} />
{/if}
{#if creating}
  <NameDialog title="New folder" okLabel="Create" onsubmit={(name) => newFolder(path, name)} onclose={() => (creating = false)} />
{/if}
{#if moving}
  <MoveDialog entry={moving} onclose={() => (moving = null)} />
{/if}
{#if account}
  <AccountSheet onclose={() => (account = false)} />
{/if}

<style>
  .files { min-height: 100dvh; padding-bottom: calc(96px + env(safe-area-inset-bottom)); }
  .files.dropping { outline: 3px dashed var(--accent); outline-offset: -8px; }

  .top {
    position: sticky;
    top: 0;
    z-index: 10;
    display: flex;
    align-items: center;
    gap: 4px;
    height: calc(var(--bar) + env(safe-area-inset-top));
    padding: env(safe-area-inset-top) 8px 0;
    background: var(--bg);
  }
  h1 {
    flex: 1;
    min-width: 0;
    margin: 0 4px;
    font-size: 1.25rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .logo {
    display: grid;
    place-items: center;
    width: 36px;
    height: 36px;
    margin: 0 4px 0 6px;
    border-radius: 10px;
    background: var(--accent);
    color: var(--accent-fg);
  }
  .avatar {
    width: 36px;
    height: 36px;
    margin: 0 4px;
    background: var(--accent-soft);
    color: var(--accent);
    font-weight: 700;
  }

  .crumbs {
    display: flex;
    align-items: center;
    gap: 2px;
    padding: 0 16px 8px;
    overflow-x: auto;
    white-space: nowrap;
    font-size: 0.9rem;
    color: var(--muted);
    scrollbar-width: none;
  }
  .crumbs a { color: var(--accent); text-decoration: none; padding: 4px 2px; }
  .crumbs span { color: var(--fg); padding: 4px 2px; }

  .banner {
    display: flex;
    align-items: center;
    gap: 10px;
    margin: 0 12px 8px;
    padding: 8px 8px 8px 14px;
    border-radius: 12px;
    background: var(--error-soft);
    color: var(--fg);
    font-size: 0.9rem;
  }
  .banner span { flex: 1; }
  .small { min-height: 36px; padding: 6px 12px; font-size: 0.85rem; }

  main { max-width: 720px; margin: 0 auto; }

  .list { list-style: none; margin: 0; padding: 0 4px; }
  .row {
    display: flex;
    align-items: center;
    border-radius: 12px;
    /* Long folders stay fast: rows off screen aren't laid out. */
    content-visibility: auto;
    contain-intrinsic-size: auto 64px;
  }
  .row:hover { background: var(--hover); }
  .open {
    display: flex;
    flex: 1;
    min-width: 0;
    align-items: center;
    gap: 14px;
    min-height: 64px;
    padding: 8px 4px 8px 12px;
    color: inherit;
    text-decoration: none;
  }
  .text { display: grid; min-width: 0; }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 500; }
  .meta { color: var(--muted); font-size: 0.85rem; font-variant-numeric: tabular-nums; }
  .more { color: var(--muted); margin-right: 4px; }

  .empty { display: grid; justify-items: center; gap: 4px; padding: 64px 24px; text-align: center; }
  .empty p { margin: 0; }
  .big { width: 64px; height: 64px; border-radius: 18px; margin-bottom: 12px; }
  .loading { text-align: center; padding: 48px; }

  .fab {
    position: fixed;
    right: 16px;
    bottom: calc(16px + env(safe-area-inset-bottom));
    z-index: 21;
    display: flex;
    align-items: center;
    gap: 10px;
    height: 56px;
    padding: 0 22px 0 18px;
    border: 0;
    border-radius: 18px;
    background: var(--accent);
    color: var(--accent-fg);
    font-weight: 700;
    font-size: 1rem;
    box-shadow: var(--shadow);
  }
</style>
