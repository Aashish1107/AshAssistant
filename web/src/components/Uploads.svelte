<script lang="ts">
  // Upload progress: a bar at the bottom while files go up, and the full
  // list when it's tapped.
  import { formatDuration, formatSize } from "../lib/paths";
  import { cancelUpload, clearFinished, retryUpload, totals, uploads } from "../lib/uploads.svelte";
  import Icon from "./Icon.svelte";

  let dialog = $state<HTMLDialogElement>();
  const sum = $derived(totals());
  const fraction = $derived(sum.size ? sum.sent / sum.size : 0);
  const summary = $derived.by(() => {
    if (sum.active) {
      const head = `Uploading ${sum.finished + 1} of ${sum.finished + sum.active}`;
      if (!sum.sending) return head + " · waiting for the connection";
      if (uploads.speed > 0) return `${head} · ${formatSize(uploads.speed)}/s · ${formatDuration((sum.size - sum.sent) / uploads.speed)} left`;
      return head;
    }
    if (sum.failed) return `${sum.failed} upload${sum.failed > 1 ? "s" : ""} failed`;
    return `All ${sum.finished} uploaded`;
  });

  $effect(() => {
    if (!dialog) return;
    if (uploads.open && !dialog.open) dialog.showModal();
    if (!uploads.open && dialog.open) dialog.close();
  });
</script>

{#if uploads.items.length}
  <button class="bar" class:failed={!sum.active && sum.failed} onclick={() => (uploads.open = true)} aria-label="Show uploads: {summary}">
    <span class="bar-icon">
      {#if sum.active}<Icon name="upload" size={20} />{:else if sum.failed}<Icon name="alert" size={20} />{:else}<Icon name="check" size={20} />{/if}
    </span>
    <span class="bar-text">{summary}</span>
    <progress max="1" value={fraction} aria-hidden="true"></progress>
  </button>
{/if}

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog bind:this={dialog} class="sheet uploads" aria-labelledby="uploads-title" onclose={() => (uploads.open = false)} onclick={(e) => e.target === dialog && dialog?.close()}>
  <div class="head">
    <h2 id="uploads-title" class="summary">{summary}</h2>
    {#if sum.finished + sum.failed > 0}
      <button class="button quiet small" onclick={clearFinished}>Clear finished</button>
    {/if}
    <button class="icon-button" aria-label="Close" onclick={() => dialog?.close()}><Icon name="close" /></button>
  </div>
  {#if sum.active}
    <p class="note muted">Keep Ash Cloud open until uploads finish. If one stops, pick the same file again and it continues where it left off.</p>
  {/if}
  <ul>
    {#each uploads.items as item (item)}
      <li class="upload" data-state={item.state}>
        <span class="name">{item.file.name}</span>
        {#if item.state === "failed"}
          <button class="button quiet small" onclick={() => retryUpload(item)}>Retry</button>
        {/if}
        {#if item.state !== "done"}
          <button class="icon-button cancel" aria-label="Cancel {item.file.name}" onclick={() => cancelUpload(item)}><Icon name="close" size={20} /></button>
        {/if}
        <progress max="1" value={item.state === "done" ? 1 : item.file.size ? item.sent / item.file.size : 0}></progress>
        <span class="meta">
          {#if item.state === "waiting"}Waiting · {formatSize(item.file.size)}
          {:else if item.state === "sending"}{Math.floor((item.sent / (item.file.size || 1)) * 100)}% · {formatSize(item.sent)} of {formatSize(item.file.size)}
          {:else if item.state === "done"}✓ {item.message}
          {:else}{item.message}{/if}
        </span>
      </li>
    {/each}
  </ul>
</dialog>

<style>
  .bar {
    position: fixed;
    left: 16px;
    right: 16px;
    bottom: calc(16px + env(safe-area-inset-bottom));
    z-index: 20;
    display: grid;
    grid-template-columns: auto 1fr;
    align-items: center;
    column-gap: 10px;
    max-width: 488px;
    margin: 0 auto;
    padding: 10px 14px 8px;
    border: 0;
    border-radius: 14px;
    background: var(--raised);
    color: var(--fg);
    box-shadow: var(--shadow);
    text-align: left;
    font-size: 0.9rem;
    font-weight: 600;
    font-variant-numeric: tabular-nums;
  }
  /* Leave room for the Upload button on the right. */
  :global(body.has-fab) .bar { right: 148px; }
  .bar-icon { color: var(--accent); }
  .bar.failed .bar-icon { color: var(--error); }
  .bar-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .bar progress { grid-column: 1 / -1; margin-top: 6px; }

  progress {
    width: 100%;
    height: 4px;
    border: 0;
    border-radius: 2px;
    background: var(--border);
    appearance: none;
    overflow: hidden;
  }
  progress::-webkit-progress-bar { background: var(--border); }
  progress::-webkit-progress-value { background: var(--accent); transition: width 0.2s; }
  progress::-moz-progress-bar { background: var(--accent); }

  .head { display: flex; align-items: center; gap: 8px; padding: 4px 8px 4px 20px; }
  .summary { flex: 1; margin: 0; font-size: 1rem; font-variant-numeric: tabular-nums; }
  .small { min-height: 36px; padding: 6px 12px; font-size: 0.85rem; }
  .note { margin: 0; padding: 0 20px 8px; font-size: 0.9rem; }

  ul { list-style: none; margin: 0; padding: 0 20px; overflow-y: auto; max-height: 60dvh; }
  .upload {
    display: grid;
    grid-template-columns: 1fr auto auto;
    align-items: center;
    column-gap: 4px;
    padding: 10px 0;
    border-top: 1px solid var(--border);
  }
  .name { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-weight: 600; }
  .cancel { width: 36px; height: 36px; color: var(--muted); }
  .upload progress { grid-column: 1 / -1; margin: 6px 0 4px; }
  .meta { grid-column: 1 / -1; color: var(--muted); font-size: 0.85rem; font-variant-numeric: tabular-nums; }
  .upload[data-state="done"] progress::-webkit-progress-value { background: var(--success); }
  .upload[data-state="done"] .meta { color: var(--success); }
  .upload[data-state="failed"] .meta { color: var(--error); }
</style>
