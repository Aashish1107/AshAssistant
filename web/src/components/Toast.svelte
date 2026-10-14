<script lang="ts">
  import { hideToast, toastState } from "../lib/toast.svelte";

  function act() {
    const action = toastState.action;
    hideToast();
    action?.run();
  }
</script>

<div class="toast" role="status" aria-live="polite" hidden={!toastState.visible}>
  <span>{toastState.text}</span>
  {#if toastState.action}
    <button onclick={act}>{toastState.action.label}</button>
  {/if}
</div>

<style>
  .toast {
    position: fixed;
    left: 50%;
    bottom: calc(96px + env(safe-area-inset-bottom));
    transform: translateX(-50%);
    z-index: 30;
    display: flex;
    align-items: center;
    gap: 12px;
    width: max-content;
    max-width: calc(100vw - 32px);
    padding: 6px 6px 6px 16px;
    min-height: 48px;
    border-radius: 12px;
    background: #26262e;
    color: #fff;
    box-shadow: var(--shadow);
    animation: rise 0.16s ease-out;
  }
  span { overflow-wrap: anywhere; }
  button {
    flex: none;
    min-height: 36px;
    padding: 6px 12px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: #a8bdff;
    font-weight: 700;
  }
  @keyframes rise { from { transform: translate(-50%, 12px); opacity: 0; } }
</style>
