<script lang="ts">
  import { untrack } from "svelte";

  // Asks for a name: a new folder's, or a new name for a file.
  let {
    title,
    value = "",
    okLabel = "OK",
    selectStem = false,
    onsubmit,
    onclose,
  }: {
    title: string;
    value?: string;
    okLabel?: string;
    /** Select the name without its extension, as a file manager would. */
    selectStem?: boolean;
    onsubmit: (name: string) => Promise<string | void>;
    onclose: () => void;
  } = $props();

  let dialog: HTMLDialogElement;
  let input: HTMLInputElement;
  let name = $state(untrack(() => value));
  let error = $state("");
  let busy = $state(false);

  $effect(() => {
    dialog.showModal();
    input.focus();
    const dot = selectStem ? value.lastIndexOf(".") : -1;
    if (dot > 0) input.setSelectionRange(0, dot);
    else input.select();
  });

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    const trimmed = name.trim();
    if (!trimmed) return;
    busy = true;
    error = "";
    const problem = await onsubmit(trimmed);
    busy = false;
    if (problem) error = problem;
    else dialog.close();
  }
</script>

<dialog bind:this={dialog} class="card" aria-labelledby="name-dialog-title" {onclose}>
  <form onsubmit={submit}>
    <h2 id="name-dialog-title">{title}</h2>
    <input bind:this={input} bind:value={name} autocomplete="off" autocapitalize="off" spellcheck="false" aria-label={title} />
    {#if error}<p class="error-text" role="alert">{error}</p>{/if}
    <div class="dialog-actions">
      <button type="button" class="button quiet" onclick={() => dialog.close()}>Cancel</button>
      <button type="submit" class="button" disabled={busy || !name.trim()}>{okLabel}</button>
    </div>
  </form>
</dialog>

<style>
  form { display: grid; gap: 12px; }
  h2 { margin: 0; font-size: 1.15rem; }
  .dialog-actions { margin-top: 4px; }
</style>
