<script lang="ts">
  import { untrack } from "svelte";
  import { move } from "../lib/actions";
  import type { Entry } from "../lib/api";
  import { baseName, parentOf } from "../lib/paths";
  import FolderPicker from "./FolderPicker.svelte";

  let { entry, onclose }: { entry: Entry; onclose: () => void } = $props();

  let dialog: HTMLDialogElement;
  const from = untrack(() => parentOf(entry.path)); // where it is now
  let folder = $state(from);
  let error = $state("");
  let busy = $state(false);

  $effect(() => dialog.showModal());

  async function here() {
    busy = true;
    error = "";
    const problem = await move(entry, folder);
    busy = false;
    if (problem) error = problem;
    else dialog.close();
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog bind:this={dialog} class="sheet" aria-labelledby="move-title" {onclose} onclick={(e) => e.target === dialog && dialog.close()}>
  <h2 id="move-title" class="sheet-title">Move {entry.name} to…</h2>
  <FolderPicker bind:folder hide={entry.type === "folder" ? entry.path : ""} />
  {#if error}<p class="error-text problem" role="alert">{error}</p>{/if}
  <div class="actions">
    <button class="button quiet" onclick={() => dialog.close()}>Cancel</button>
    <button class="button" onclick={here} disabled={busy || folder === from}>
      {folder === from ? "Choose a folder" : `Move to ${folder === "/" ? "Home" : baseName(folder)}`}
    </button>
  </div>
</dialog>

<style>
  .actions { display: flex; justify-content: flex-end; gap: 8px; padding: 12px 20px 4px; }
  .actions .button:last-child { max-width: 65%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: block; }
  .problem { padding: 8px 20px 0; }
</style>
