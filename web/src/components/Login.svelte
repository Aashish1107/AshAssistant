<script lang="ts">
  import { ApiError, messageOf } from "../lib/api";
  import { checkSession, logIn, session } from "../lib/session.svelte";
  import Icon from "./Icon.svelte";

  let username = $state("");
  let password = $state("");
  let error = $state("");
  let busy = $state(false);

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    error = "";
    busy = true;
    try {
      await logIn(username.trim(), password);
      password = "";
    } catch (problem) {
      error = problem instanceof ApiError && problem.code === "invalid_credentials" ? "Wrong username or password" : messageOf(problem);
    } finally {
      busy = false;
    }
  }

  async function retry() {
    busy = true;
    await checkSession();
    busy = false;
  }
</script>

<main class="login">
  <div class="brand">
    <span class="logo"><Icon name="cloud" size={36} /></span>
    <h1>Ash Cloud</h1>
    <p class="muted">Your laptop's files, anywhere.</p>
  </div>

  {#if session.offline}
    <section class="panel" aria-live="polite">
      <p class="offline"><Icon name="wifi" /> Can't reach the laptop.</p>
      <p class="muted">Check that the Tailscale app is on, and that the laptop is awake with Ash Cloud running.</p>
      <button class="button" onclick={retry} disabled={busy}><Icon name="refresh" size={20} /> Try again</button>
    </section>
  {:else}
    <form class="panel" onsubmit={submit}>
      <label>
        <span>Username</span>
        <input bind:value={username} name="username" autocomplete="username" autocapitalize="none" autocorrect="off" spellcheck="false" required />
      </label>
      <label>
        <span>Password</span>
        <input bind:value={password} name="password" type="password" autocomplete="current-password" required />
      </label>
      {#if error}<p class="error-text" role="alert">{error}</p>{/if}
      <button class="button" type="submit" disabled={busy}>{busy ? "Logging in…" : "Log in"}</button>
    </form>
  {/if}
</main>

<style>
  .login {
    display: grid;
    align-content: center;
    gap: 28px;
    min-height: 100dvh;
    max-width: 400px;
    margin: 0 auto;
    padding: 24px 20px calc(24px + env(safe-area-inset-bottom));
  }

  .brand { text-align: center; }

  .logo {
    display: inline-grid;
    place-items: center;
    width: 64px;
    height: 64px;
    border-radius: 18px;
    background: var(--accent);
    color: var(--accent-fg);
  }

  h1 { margin: 12px 0 2px; font-size: 1.75rem; }
  .brand p { margin: 0; }

  .panel {
    display: grid;
    gap: 16px;
    padding: 20px;
    border-radius: var(--radius);
    background: var(--card);
    border: 1px solid var(--border);
  }

  label { display: grid; gap: 6px; font-weight: 600; }

  .offline { display: flex; align-items: center; gap: 10px; margin: 0; font-weight: 600; }
  .panel p { margin: 0; }
</style>
