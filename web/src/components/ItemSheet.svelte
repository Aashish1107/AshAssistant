<script lang="ts" module>
  export type ItemAction = "rename" | "move" | "delete";
</script>

<script lang="ts">
  // What you can do with a file or folder: the ⋯ menu.
  import type { Entry } from "../lib/api";
  import { contentURL, formatDate, formatSize, kindOf } from "../lib/paths";
  import Icon from "./Icon.svelte";
  import TypeIcon from "./TypeIcon.svelte";

  let { entry, onaction, onclose }: { entry: Entry; onaction: (action: ItemAction) => void; onclose: () => void } = $props();

  let dialog: HTMLDialogElement;
  $effect(() => dialog.showModal());

  function choose(action: ItemAction) {
    dialog.close();
    onaction(action);
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog bind:this={dialog} class="sheet" aria-labelledby="item-sheet-title" {onclose} onclick={(e) => e.target === dialog && dialog.close()}>
  <div class="sheet-title">
    <TypeIcon kind={kindOf(entry)} />
    <div class="what">
      <span id="item-sheet-title">{entry.name}</span>
      <span class="muted">{entry.type === "file" ? formatSize(entry.size ?? 0) + " · " : ""}{formatDate(entry.modified)}</span>
    </div>
  </div>
  {#if entry.type === "file"}
    <a class="sheet-item" href={contentURL(entry.path)} download={entry.name} onclick={() => dialog.close()}><Icon name="download" /> Download</a>
  {/if}
  <button class="sheet-item" onclick={() => choose("rename")}><Icon name="pencil" /> Rename</button>
  <button class="sheet-item" onclick={() => choose("move")}><Icon name="move" /> Move to…</button>
  <button class="sheet-item danger" onclick={() => choose("delete")}><Icon name="trash" /> Delete</button>
</dialog>

<style>
  .what { display: grid; min-width: 0; }
  .what .muted { font-weight: 400; font-size: 0.85rem; }
</style>
