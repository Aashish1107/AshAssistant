<script lang="ts">
  import { whenLoggedOut } from "./lib/api";
  import { folder, forgetFolders, openFolder, refreshFolder } from "./lib/folders.svelte";
  import { currentFolder, router } from "./lib/router.svelte";
  import { checkSession, session } from "./lib/session.svelte";
  import { whenUploaded } from "./lib/uploads.svelte";
  import Files from "./components/Files.svelte";
  import Icon from "./components/Icon.svelte";
  import Login from "./components/Login.svelte";
  import Player from "./components/Player.svelte";
  import Share from "./components/Share.svelte";
  import Toast from "./components/Toast.svelte";
  import Uploads from "./components/Uploads.svelte";
  import Viewer from "./components/Viewer.svelte";

  whenLoggedOut(() => {
    session.current = null;
    forgetFolders();
  });

  // Several uploads finishing together reload the folder once.
  let refreshTimer = 0;
  whenUploaded((uploadedTo) => {
    if (uploadedTo !== folder.path) return;
    clearTimeout(refreshTimer);
    refreshTimer = window.setTimeout(refreshFolder, 300);
  });

  // Ask who's logged in and fetch the folder at the same time: one round
  // trip to the laptop instead of two before anything shows.
  checkSession();
  if (router.route.view === "folder" || router.route.view === "viewer") openFolder(currentFolder());

  const route = $derived(router.route);
</script>

{#if !session.checked}
  <div class="splash" aria-label="Loading"><span class="logo"><Icon name="cloud" size={40} /></span></div>
{:else if !session.current}
  <Login />
{:else}
  {#if route.view === "player"}
    {#key route.path}<Player path={route.path} />{/key}
  {:else if route.view === "share"}
    <Share />
  {:else}
    <Files />
    {#if route.view === "viewer"}<Viewer path={route.path} />{/if}
  {/if}
  {#if route.view !== "player" && route.view !== "viewer"}<Uploads />{/if}
{/if}
<Toast />

<style>
  .splash { display: grid; place-items: center; min-height: 100dvh; }
  .logo {
    display: grid;
    place-items: center;
    width: 72px;
    height: 72px;
    border-radius: 20px;
    background: var(--accent);
    color: var(--accent-fg);
  }
</style>
