<script lang="ts">
  // Photos and videos shared to Ash Cloud from another app, such as the
  // gallery: pick a folder, and they upload there.
  import { sharedFiles } from "../lib/app.svelte";
  import { baseName, formatSize } from "../lib/paths";
  import { go, link } from "../lib/router.svelte";
  import { addUploads, uploads } from "../lib/uploads.svelte";
  import FolderPicker from "./FolderPicker.svelte";
  import Icon from "./Icon.svelte";

  let files = $state<File[] | null>(null);
  let folder = $state("/");

  $effect(() => {
    sharedFiles().then((shared) => (files = shared));
  });

  const total = $derived((files ?? []).reduce((sum, file) => sum + file.size, 0));

  function upload() {
    if (!files?.length) return;
    addUploads(files, folder);
    uploads.open = true;
    go(link.folder(folder), { replace: true });
  }
</script>

<div class="share">
  <header class="top">
    <a class="icon-button" href={link.folder("/")} aria-label="Cancel"><Icon name="close" /></a>
    <h1>{files === null ? "Shared files" : `Upload ${files.length} file${files.length === 1 ? "" : "s"}`}</h1>
  </header>

  {#if files === null}
    <p class="note muted">Getting the shared files…</p>
  {:else if files.length === 0}
    <p class="note">Nothing was shared, or it was already uploaded. <a href={link.folder("/")}>Go to your files</a></p>
  {:else}
    <p class="note muted">{files.map((f) => f.name).slice(0, 3).join(", ")}{files.length > 3 ? ` and ${files.length - 3} more` : ""} · {formatSize(total)}</p>
    <h2>Choose a folder</h2>
    <FolderPicker bind:folder />
    <div class="actions">
      <button class="button" onclick={upload}><Icon name="upload" size={20} /> Upload to {folder === "/" ? "Home" : baseName(folder)}</button>
    </div>
  {/if}
</div>

<style>
  .share { max-width: 600px; margin: 0 auto; min-height: 100dvh; }
  .top {
    display: flex;
    align-items: center;
    gap: 4px;
    height: calc(var(--bar) + env(safe-area-inset-top));
    padding: env(safe-area-inset-top) 8px 0;
  }
  h1 { margin: 0 4px; font-size: 1.2rem; }
  h2 { margin: 8px 20px; font-size: 1rem; }
  .note { margin: 0; padding: 4px 20px 12px; overflow-wrap: anywhere; }
  .actions { padding: 16px 20px; }
  .actions .button { width: 100%; }
</style>
