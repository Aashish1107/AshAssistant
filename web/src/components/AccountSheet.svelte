<script lang="ts">
  import { messageOf } from "../lib/api";
  import { app, install } from "../lib/app.svelte";
  import { forgetFolders } from "../lib/folders.svelte";
  import { logOut, session } from "../lib/session.svelte";
  import Icon from "./Icon.svelte";

  let { onclose }: { onclose: () => void } = $props();

  let dialog: HTMLDialogElement;
  let error = $state("");
  $effect(() => dialog.showModal());

  const user = $derived(session.current!.user);
  const until = $derived(new Date(session.current!.expires_at).toLocaleDateString(undefined, { day: "numeric", month: "long", year: "numeric" }));

  async function leave() {
    try {
      await logOut();
      forgetFolders();
      dialog.close();
    } catch (problem) {
      error = messageOf(problem);
    }
  }
</script>

<!-- svelte-ignore a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
<dialog bind:this={dialog} class="sheet" aria-labelledby="account-title" {onclose} onclick={(e) => e.target === dialog && dialog.close()}>
  <div class="who">
    <span class="avatar" aria-hidden="true">{user.username.slice(0, 1).toUpperCase()}</span>
    <div>
      <h2 id="account-title">{user.username}</h2>
      <p class="muted">{user.role === "admin" ? "Admin" : "User"} · stays logged in on this phone until {until}, and longer while you use it</p>
    </div>
  </div>
  {#if app.installable}
    <button class="sheet-item" onclick={install}><Icon name="install" /> Add Ash Cloud to the home screen</button>
  {/if}
  <button class="sheet-item" onclick={leave}><Icon name="logout" /> Log out of this phone</button>
  {#if error}<p class="error-text problem" role="alert">{error}</p>{/if}
</dialog>

<style>
  .who { display: flex; gap: 14px; align-items: center; padding: 12px 20px 16px; }
  .who h2 { margin: 0; font-size: 1.1rem; }
  .who p { margin: 2px 0 0; font-size: 0.9rem; }
  .avatar {
    display: grid;
    place-items: center;
    flex: none;
    width: 48px;
    height: 48px;
    border-radius: 50%;
    background: var(--accent);
    color: var(--accent-fg);
    font-weight: 700;
    font-size: 1.25rem;
  }
  .problem { padding: 8px 20px; }
</style>
