<script lang="ts">
  // Photos, full screen. Swipe (or ← →) for the next and previous photo in
  // the folder; pinch to zoom.
  import { remove, rename } from "../lib/actions";
  import type { Entry } from "../lib/api";
  import { folder } from "../lib/folders.svelte";
  import { baseName, contentURL, joinPath, parentOf, viewable } from "../lib/paths";
  import { go, goBack, link } from "../lib/router.svelte";
  import Icon from "./Icon.svelte";
  import ItemSheet, { type ItemAction } from "./ItemSheet.svelte";
  import MoveDialog from "./MoveDialog.svelte";
  import NameDialog from "./NameDialog.svelte";

  let { path }: { path: string } = $props();

  const photos = $derived(folder.entries.filter(viewable));
  const index = $derived(photos.findIndex((e) => e.path === path));
  const entry = $derived<Entry>(
    photos[index] ?? { name: baseName(path), path, type: "file", modified: "", content_type: "image/jpeg" },
  );
  let loaded = $state(false);
  let failed = $state(false);
  let menu = $state(false);
  let renaming = $state(false);
  let moving = $state(false);

  $effect(() => {
    void path;
    loaded = false;
    failed = false;
    // Fetch the neighbours now, so swiping shows them at once.
    for (const near of [photos[index + 1], photos[index - 1]]) {
      if (near) new Image().src = contentURL(near.path, true);
    }
  });

  function show(offset: number) {
    const next = photos[index + offset];
    if (next) go(link.viewer(next.path), { replace: true });
  }

  function close() {
    goBack(link.folder(parentOf(path)));
  }

  // Swipes. Ignored while zoomed in, when a drag pans the photo instead.
  let startX = 0;
  let startY = 0;
  function down(event: PointerEvent) {
    startX = event.clientX;
    startY = event.clientY;
  }
  function up(event: PointerEvent) {
    if ((window.visualViewport?.scale ?? 1) > 1.01) return;
    const dx = event.clientX - startX;
    const dy = event.clientY - startY;
    if (Math.abs(dx) > 50 && Math.abs(dx) > Math.abs(dy) * 1.5) show(dx < 0 ? 1 : -1);
    else if (dy > 120 && Math.abs(dy) > Math.abs(dx) * 1.5) close(); // swipe down to close
  }

  function key(event: KeyboardEvent) {
    if (menu || renaming || moving) return;
    if (event.key === "ArrowRight") show(1);
    else if (event.key === "ArrowLeft") show(-1);
    else if (event.key === "Escape") close();
  }

  async function act(action: ItemAction) {
    if (action === "rename") renaming = true;
    if (action === "move") moving = true;
    if (action === "delete") {
      const next = photos[index + 1] ?? photos[index - 1];
      const gone = entry;
      if (next) go(link.viewer(next.path), { replace: true });
      else close();
      await remove(gone);
    }
  }
</script>

<svelte:window onkeydown={key} />

<div class="viewer" role="dialog" aria-modal="true" aria-label="Photo: {entry.name}">
  <header>
    <button class="icon-button" aria-label="Close" onclick={close}><Icon name="back" /></button>
    <div class="title">
      <span class="name">{entry.name}</span>
      {#if index >= 0 && photos.length > 1}<span class="count">{index + 1} of {photos.length}</span>{/if}
    </div>
    <a class="icon-button" href={contentURL(path)} download={entry.name} aria-label="Download"><Icon name="download" /></a>
    <button class="icon-button" aria-label="More" onclick={() => (menu = true)}><Icon name="more" /></button>
  </header>

  <!-- svelte-ignore a11y_no_static_element_interactions -->
  <div class="stage" onpointerdown={down} onpointerup={up}>
    {#key path}
      <img src={contentURL(path, true)} alt={entry.name} class:loaded onload={() => (loaded = true)} onerror={() => (failed = true)} draggable="false" />
    {/key}
    {#if failed}
      <p class="note">This photo can't be shown in the browser. <a href={contentURL(path)} download={entry.name}>Download it</a> instead.</p>
    {:else if !loaded}
      <p class="note" aria-live="polite">Loading…</p>
    {/if}
  </div>

  {#if index > 0}
    <button class="nav prev icon-button" aria-label="Previous photo" onclick={() => show(-1)}><Icon name="back" /></button>
  {/if}
  {#if index >= 0 && index < photos.length - 1}
    <button class="nav next icon-button" aria-label="Next photo" onclick={() => show(1)}><Icon name="chevron" /></button>
  {/if}
</div>

{#if menu}
  <ItemSheet {entry} onaction={act} onclose={() => (menu = false)} />
{/if}
{#if renaming}
  <NameDialog
    title="Rename"
    value={entry.name}
    okLabel="Rename"
    selectStem
    onsubmit={async (name) => {
      const problem = await rename(entry, name);
      if (!problem) go(link.viewer(joinPath(parentOf(path), name)), { replace: true });
      return problem;
    }}
    onclose={() => (renaming = false)}
  />
{/if}
{#if moving}
  <MoveDialog entry={entry} onclose={() => { moving = false; if (!photos.some((p) => p.path === path)) close(); }} />
{/if}

<style>
  .viewer {
    position: fixed;
    inset: 0;
    z-index: 25;
    display: grid;
    grid-template-rows: auto 1fr;
    background: #000;
    color: #fff;
  }
  header {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    gap: 4px;
    padding: calc(4px + env(safe-area-inset-top)) 4px 16px;
    background: linear-gradient(rgba(0, 0, 0, 0.6), transparent);
  }
  .icon-button { color: #fff; }
  .icon-button:hover { background: rgba(255, 255, 255, 0.12); }
  .title { flex: 1; display: grid; min-width: 0; }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .count { font-size: 0.8rem; color: #c8c8d2; }
  .stage {
    grid-row: 1 / -1;
    display: grid;
    place-items: center;
    min-height: 0;
    overflow: hidden;
    touch-action: pan-y pinch-zoom;
  }
  img {
    grid-area: 1 / 1;
    max-width: 100%;
    max-height: 100dvh;
    object-fit: contain;
    opacity: 0;
    transition: opacity 0.12s;
    user-select: none;
  }
  img.loaded { opacity: 1; }
  .note { grid-area: 1 / 1; margin: 0; padding: 24px; text-align: center; color: #d8d8e0; }
  .note a { color: #a8bdff; }
  .nav {
    position: absolute;
    top: 50%;
    transform: translateY(-50%);
    background: rgba(0, 0, 0, 0.35);
  }
  .prev { left: 8px; }
  .next { right: 8px; }
  @media (pointer: coarse) {
    .nav { display: none; } /* swipe instead */
  }
</style>
