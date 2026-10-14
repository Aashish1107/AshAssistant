<script lang="ts">
  // The video player: −10 / +10, a slider that jumps anywhere without
  // downloading the rest, full screen with the controls still visible, and a
  // choice of audio track for videos that have more than one.
  import { api, messageOf, type AudioPrepare, type AudioTrack, type MediaInfo } from "../lib/api";
  import { AudioSync } from "../lib/audiosync";
  import { baseName, contentURL, formatClock, parentOf } from "../lib/paths";
  import { goBack, link } from "../lib/router.svelte";
  import Icon from "./Icon.svelte";

  let { path }: { path: string } = $props();

  const SKIP_SECONDS = 10;
  const name = $derived(baseName(path));

  let video: HTMLVideoElement;
  let stage: HTMLDivElement;
  let sync: AudioSync;

  let playing = $state(false);
  let position = $state(0);
  let duration = $state(NaN);
  let buffering = $state(false);
  let dragging = $state(false);
  let fullscreen = $state(false);

  let info = $state<MediaInfo | null>(null);
  let current = $state(0); // the audio track being heard
  let pending = $state<number | null>(null); // a track being prepared
  let status = $state("");
  let menuOpen = $state(false);
  let token = 0; // bumped to cancel an older track's preparation

  $effect(() => {
    sync = new AudioSync(video);
    const onFullscreen = () => {
      fullscreen = document.fullscreenElement === stage;
      if (!document.fullscreenElement) {
        try {
          screen.orientation?.unlock?.();
        } catch {
          // not supported
        }
      }
    };
    document.addEventListener("fullscreenchange", onFullscreen);
    loadTracks();
    return () => {
      token++;
      sync.destroy();
      document.removeEventListener("fullscreenchange", onFullscreen);
      if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
      video.pause();
      video.removeAttribute("src");
      video.load();
    };
  });

  async function loadTracks() {
    try {
      info = await api<MediaInfo>("GET", "/api/v1/media/info?path=" + encodeURIComponent(path));
      current = info.audio_tracks.find((t) => t.native)?.track ?? 0;
    } catch (error) {
      info = { audio_tracks: [], can_switch_audio: false, reason: messageOf(error) };
    }
  }

  // ---- controls ----

  function togglePlay() {
    if (video.paused || video.ended) video.play().catch(() => {});
    else video.pause();
  }

  function skip(seconds: number) {
    if (!Number.isFinite(video.duration)) return;
    video.currentTime = Math.min(Math.max(0, video.currentTime + seconds), video.duration);
  }

  // Dragging the slider moves the video at most 4 times a second, then
  // exactly where it's released.
  let lastSeek = 0;
  function sliding(event: Event) {
    dragging = true;
    position = Number((event.currentTarget as HTMLInputElement).value);
    const now = Date.now();
    if (now - lastSeek > 250) {
      lastSeek = now;
      video.currentTime = position;
    }
  }
  function slid(event: Event) {
    dragging = false;
    video.currentTime = Number((event.currentTarget as HTMLInputElement).value);
  }

  async function toggleFullscreen() {
    if (document.fullscreenElement) {
      await document.exitFullscreen().catch(() => {});
      return;
    }
    if (stage.requestFullscreen) {
      await stage.requestFullscreen({ navigationUI: "hide" }).catch(() => {});
      (screen.orientation as any)?.lock?.("landscape").catch(() => {});
    } else if ((video as any).webkitEnterFullscreen) {
      (video as any).webkitEnterFullscreen(); // iPhone: the system player
    }
  }

  function key(event: KeyboardEvent) {
    const target = event.target as HTMLElement;
    if (target.matches("input:not([type=range]), textarea")) return;
    if (event.key === "ArrowLeft") skip(-SKIP_SECONDS);
    else if (event.key === "ArrowRight") skip(SKIP_SECONDS);
    else if (event.key === " " && target === document.body) togglePlay();
    else return;
    event.preventDefault();
  }

  // ---- audio tracks ----

  // Turns language codes such as "hin" into names such as "Hindi". An odd
  // browser language setting can make the constructor throw.
  const languages = (() => {
    for (const locales of [navigator.languages, undefined]) {
      try {
        return new Intl.DisplayNames(locales as string[] | undefined, { type: "language" });
      } catch {
        // try the next
      }
    }
    return null;
  })();

  function trackName(track: AudioTrack): string {
    if (track.title) return track.title;
    if (track.language) {
      try {
        return languages?.of(track.language) ?? track.language;
      } catch {
        return track.language;
      }
    }
    return `Track ${track.track + 1}`;
  }

  function trackDetail(track: AudioTrack): string {
    const channels = ({ 1: "mono", 2: "stereo", 6: "5.1", 8: "7.1" } as Record<number, string>)[track.channels] ?? `${track.channels} ch`;
    return `${channels} · ${track.codec.toUpperCase()}`;
  }

  const currentTrack = $derived(info?.audio_tracks[current]);

  async function selectTrack(number: number) {
    menuOpen = false;
    const track = info?.audio_tracks[number];
    if (!track || (number === current && pending === null)) return;
    const mine = ++token;
    pending = null;
    if (track.native) {
      sync.detach();
      current = number;
      status = "";
      return;
    }
    // Keep playing the current audio until the new track is ready.
    pending = number;
    for (;;) {
      let result: AudioPrepare;
      try {
        result = await api<AudioPrepare>("POST", "/api/v1/media/audio", { path, track: number });
      } catch (error) {
        result = { state: "failed", error: messageOf(error) };
      }
      if (mine !== token) return; // another choice replaced this one
      if (result.state === "failed") {
        pending = null;
        status = `Couldn't switch to ${trackName(track)}: ${result.error ?? "unknown problem"}`;
        return;
      }
      if (result.state === "ready" && result.url) {
        pending = null;
        current = number;
        sync.attach(result.url);
        status = "";
        return;
      }
      status = `Preparing ${trackName(track)} audio… ${Math.round((result.progress ?? 0) * 100)}% (only the first time)`;
      await new Promise((resolve) => setTimeout(resolve, 1000));
    }
  }
</script>

<svelte:window onkeydown={key} />
<svelte:document onclick={() => (menuOpen = false)} />

<div class="player">
  <header class="top">
    <button class="icon-button" aria-label="Back to the folder" onclick={() => goBack(link.folder(parentOf(path)))}><Icon name="back" /></button>
    <h1>{name}</h1>
    <a class="icon-button" href={contentURL(path)} download={name} aria-label="Download"><Icon name="download" /></a>
  </header>

  <div class="stage" class:fullscreen bind:this={stage}>
    <!-- svelte-ignore a11y_media_has_caption, a11y_click_events_have_key_events, a11y_no_noninteractive_element_interactions -->
    <video
      bind:this={video}
      src={contentURL(path, true)}
      playsinline
      preload="metadata"
      onclick={togglePlay}
      onplay={() => (playing = true)}
      onpause={() => (playing = false)}
      onended={() => (playing = false)}
      onloadedmetadata={() => (duration = video.duration)}
      ontimeupdate={() => {
        if (!dragging) position = video.currentTime;
      }}
      onwaiting={() => (buffering = true)}
      onplaying={() => (buffering = false)}
      oncanplay={() => (buffering = false)}
    ></video>
    {#if buffering}<p class="buffering" aria-live="polite">Loading…</p>{/if}

    <div class="controls">
      <div class="seek-row">
        <input
          type="range"
          min="0"
          max={Number.isFinite(duration) ? duration : 0}
          step="0.1"
          value={position}
          aria-label="Position in the video"
          oninput={sliding}
          onchange={slid}
        />
        <span class="clock">{formatClock(position)} / {formatClock(duration)}</span>
      </div>
      <div class="control-row">
        <button class="text-button" aria-label="Back 10 seconds" onclick={() => skip(-SKIP_SECONDS)}>−10</button>
        <button class="icon-button" aria-label={playing ? "Pause" : "Play"} onclick={togglePlay}><Icon name={playing ? "pause" : "play"} size={28} /></button>
        <button class="text-button" aria-label="Forward 10 seconds" onclick={() => skip(SKIP_SECONDS)}>+10</button>
        <span class="spacer"></span>
        <button
          class="audio-button"
          aria-haspopup="menu"
          aria-expanded={menuOpen}
          title={currentTrack ? `${trackName(currentTrack)} (${trackDetail(currentTrack)})` : "Audio track"}
          onclick={(e) => {
            e.stopPropagation();
            menuOpen = !menuOpen;
          }}
        >
          <Icon name="headphones" size={20} />
          <span>{currentTrack ? trackName(currentTrack) : "Audio"}</span>
        </button>
        <button class="icon-button" aria-label={fullscreen ? "Leave full screen" : "Full screen"} onclick={toggleFullscreen}><Icon name="fullscreen" /></button>
      </div>

      {#if menuOpen}
        <!-- svelte-ignore a11y_click_events_have_key_events -->
        <div class="menu" role="menu" aria-label="Audio track" tabindex="-1" onclick={(e) => e.stopPropagation()}>
          {#if !info}
            <p class="menu-note">Reading the video's audio tracks…</p>
          {:else}
            {#each info.audio_tracks as track (track.track)}
              {@const selected = track.track === current && pending === null}
              <button class="menu-item" role="menuitemradio" aria-checked={selected} disabled={!info.can_switch_audio} onclick={() => selectTrack(track.track)}>
                <span class="check">{#if selected}<Icon name="check" size={18} />{:else if track.track === pending}…{/if}</span>
                <span>{trackName(track)}</span>
                <span class="detail">{trackDetail(track)}</span>
              </button>
            {/each}
            {#if !info.can_switch_audio}
              <p class="menu-note">{info.reason || "This video's audio can't be switched"}</p>
            {/if}
          {/if}
        </div>
      {/if}
    </div>
  </div>

  {#if status}<p class="status" role="status">{status}</p>{/if}
</div>

<style>
  .player { min-height: 100dvh; background: #000; color: #fff; }
  .top {
    display: flex;
    align-items: center;
    gap: 4px;
    height: calc(var(--bar) + env(safe-area-inset-top));
    padding: env(safe-area-inset-top) 4px 0;
  }
  .top h1 {
    flex: 1;
    min-width: 0;
    margin: 0 4px;
    font-size: 1.05rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .icon-button { color: #fff; }
  .icon-button:hover { background: rgba(255, 255, 255, 0.12); }

  .stage { position: relative; display: flex; flex-direction: column; background: #000; }
  video { display: block; width: 100%; max-height: 70dvh; background: #000; }
  .buffering {
    position: absolute;
    top: 40%;
    left: 0;
    right: 0;
    margin: 0;
    text-align: center;
    text-shadow: 0 1px 3px #000;
    pointer-events: none;
  }

  .controls { position: relative; padding: 4px 8px calc(8px + env(safe-area-inset-bottom)); background: #111; }
  .seek-row { display: flex; align-items: center; gap: 10px; }
  .seek-row input { flex: 1; min-width: 0; margin: 10px 0; accent-color: #7b9bff; }
  .clock { flex: none; font-size: 0.8rem; font-variant-numeric: tabular-nums; color: #d8d8e0; }
  .control-row { display: flex; align-items: center; gap: 4px; }
  .spacer { flex: 1; }
  .text-button {
    min-width: 48px;
    min-height: 44px;
    padding: 0 8px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: #fff;
    font-weight: 700;
    font-variant-numeric: tabular-nums;
  }
  .text-button:active, .audio-button:active { background: rgba(255, 255, 255, 0.15); }
  .audio-button {
    display: flex;
    align-items: center;
    gap: 6px;
    max-width: 10em;
    min-height: 44px;
    padding: 0 10px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: #fff;
    font-size: 0.85rem;
    font-weight: 600;
  }
  .audio-button span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .menu {
    position: absolute;
    right: 8px;
    bottom: 100%;
    z-index: 3;
    min-width: 240px;
    max-width: calc(100% - 16px);
    max-height: 60dvh;
    overflow-y: auto;
    padding: 4px;
    border: 1px solid #3a3a44;
    border-radius: 12px;
    background: #1f1f26;
    box-shadow: 0 8px 24px rgba(0, 0, 0, 0.5);
  }
  .menu-item {
    display: grid;
    grid-template-columns: 1.5em 1fr auto;
    align-items: center;
    gap: 8px;
    width: 100%;
    min-height: 44px;
    padding: 8px;
    border: 0;
    border-radius: 8px;
    background: transparent;
    color: #fff;
    text-align: left;
  }
  .menu-item:hover:not(:disabled) { background: rgba(255, 255, 255, 0.1); }
  .menu-item:disabled { opacity: 0.6; cursor: default; }
  .detail { color: #a8a8b5; font-size: 0.8rem; }
  .menu-note { margin: 8px; color: #c8c8d2; font-size: 0.85rem; }
  .status { margin: 0; padding: 12px 16px; color: #d8d8e0; font-size: 0.9rem; }

  /* Full screen: the video fills the screen, the controls sit on top. */
  .stage.fullscreen { justify-content: center; height: 100%; }
  .stage.fullscreen video { flex: 1; min-height: 0; max-height: none; object-fit: contain; }
  .stage.fullscreen .controls { background: rgba(0, 0, 0, 0.6); }
</style>
