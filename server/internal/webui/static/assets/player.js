"use strict";

// Video player with −10 s / +10 s buttons and an audio track menu.
//
// Chrome on Android only plays a video's first audio track. To hear another
// one, the server extracts that track to its own file; the player then mutes
// the video and plays the extracted track alongside it, keeping the two in
// sync through play, pause, skips, scrubbing and buffering.

const byId = (id) => document.getElementById(id);
const video = byId("video");

const player = {
  path: null,          // the video's path, e.g. "/Videos/movie.mp4"
  info: null,          // GET /api/v1/media/info response
  current: 0,          // the audio track being heard
  pending: null,       // a track being prepared
  pollToken: 0,        // bumped to cancel an older track's preparation
  external: null,      // <audio> playing an extracted track, or null
  driftTimer: null,
  resumeAfterAudio: false,
  seekThrottle: 0,
};

const SKIP_SECONDS = 10;
// Turns language codes such as "hin" into names such as "Hindi". Built
// defensively: an odd browser language setting (e.g. "en-US@posix") makes
// the constructor throw, and that must not stop the player from loading.
const languageNames = (() => {
  for (const locales of [navigator.languages, undefined]) {
    try { return new Intl.DisplayNames(locales, { type: "language" }); } catch { /* try the next */ }
  }
  return null;
})();

// ---- opening and closing ---------------------------------------------------

function openPlayer(path) {
  if (player.path === path) return;
  closePlayer();
  player.path = path;
  const name = path.split("/").pop();
  const folder = path.slice(0, path.lastIndexOf("/")) || "/";
  byId("player-title").textContent = name;
  byId("player-back").href = folderLink(folder);
  byId("player-download").href = contentLink(path, false);
  video.src = contentLink(path, true);
  renderAudioButton();
  loadAudioTracks(path);
}

function closePlayer() {
  stopPreparing();
  detachExternal();
  if (document.fullscreenElement) document.exitFullscreen().catch(() => {});
  player.path = null;
  player.info = null;
  player.current = 0;
  closeAudioMenu();
  setAudioStatus("");
  video.pause();
  video.removeAttribute("src");
  video.load();
  byId("seek").value = 0;
  byId("seek").max = 0;
  updateClock();
}

async function loadAudioTracks(path) {
  const { response, data } = await api("GET", "/api/v1/media/info?path=" + encodeURIComponent(path));
  if (player.path !== path) return; // the user moved on
  if (!response.ok) {
    player.info = { audio_tracks: [], can_switch_audio: false, reason: data?.error?.message ?? "Couldn't read the video's audio tracks" };
  } else {
    player.info = data;
    const native = data.audio_tracks.find((t) => t.native);
    player.current = native ? native.track : 0;
  }
  renderAudioButton();
}

// ---- controls ----------------------------------------------------------------

function togglePlay() {
  if (video.paused || video.ended) video.play().catch(() => {});
  else video.pause();
}

function skip(seconds) {
  if (!Number.isFinite(video.duration)) return;
  video.currentTime = Math.min(Math.max(0, video.currentTime + seconds), video.duration);
}

function formatTime(seconds) {
  if (!Number.isFinite(seconds)) return "0:00";
  const s = Math.floor(seconds % 60).toString().padStart(2, "0");
  const m = Math.floor(seconds / 60) % 60;
  const h = Math.floor(seconds / 3600);
  return h > 0 ? `${h}:${m.toString().padStart(2, "0")}:${s}` : `${m}:${s}`;
}

function updateClock(position = video.currentTime) {
  byId("clock").textContent = `${formatTime(position)} / ${formatTime(video.duration)}`;
}

function updatePlayButton() {
  const playing = !video.paused && !video.ended;
  byId("playpause").textContent = playing ? "❚❚" : "▶";
  byId("playpause").setAttribute("aria-label", playing ? "Pause" : "Play");
}

byId("playpause").addEventListener("click", togglePlay);
video.addEventListener("click", togglePlay);
byId("back10").addEventListener("click", () => skip(-SKIP_SECONDS));
byId("fwd10").addEventListener("click", () => skip(SKIP_SECONDS));

video.addEventListener("loadedmetadata", () => {
  byId("seek").max = video.duration;
  updateClock();
});
video.addEventListener("timeupdate", () => {
  if (!byId("seek").matches(":active")) byId("seek").value = video.currentTime;
  updateClock();
});
video.addEventListener("play", updatePlayButton);
video.addEventListener("pause", updatePlayButton);
video.addEventListener("ended", updatePlayButton);
video.addEventListener("waiting", () => { byId("buffering").hidden = false; });
video.addEventListener("playing", () => { byId("buffering").hidden = true; });
video.addEventListener("canplay", () => { byId("buffering").hidden = true; });

// Dragging the slider moves the video (at most 4 times a second while
// dragging, then exactly where it's released).
byId("seek").addEventListener("input", (event) => {
  const position = Number(event.target.value);
  updateClock(position);
  const now = Date.now();
  if (now - player.seekThrottle > 250) {
    player.seekThrottle = now;
    video.currentTime = position;
  }
});
byId("seek").addEventListener("change", (event) => {
  video.currentTime = Number(event.target.value);
});

// Full screen covers the whole player, so −10, +10 and Audio stay visible.
byId("fullscreen").addEventListener("click", async () => {
  if (document.fullscreenElement) {
    await document.exitFullscreen().catch(() => {});
    return;
  }
  const stage = byId("stage");
  if (stage.requestFullscreen) {
    await stage.requestFullscreen({ navigationUI: "hide" }).catch(() => {});
    if (screen.orientation?.lock) screen.orientation.lock("landscape").catch(() => {});
  } else if (video.webkitEnterFullscreen) {
    video.webkitEnterFullscreen(); // iPhone: the system player
  }
});
document.addEventListener("fullscreenchange", () => {
  if (!document.fullscreenElement && screen.orientation?.unlock) {
    try { screen.orientation.unlock(); } catch { /* not supported */ }
  }
});

// Keyboard, on a laptop: ← / → skip, space plays or pauses.
document.addEventListener("keydown", (event) => {
  if (!player.path || event.target.matches("input:not([type=range]), textarea")) return;
  if (event.key === "ArrowLeft") { skip(-SKIP_SECONDS); event.preventDefault(); }
  if (event.key === "ArrowRight") { skip(SKIP_SECONDS); event.preventDefault(); }
  if (event.key === " " && event.target === document.body) { togglePlay(); event.preventDefault(); }
});

// ---- audio tracks ------------------------------------------------------------

function trackName(track) {
  if (track.title) return track.title;
  if (track.language) {
    try { return languageNames?.of(track.language) ?? track.language; } catch { return track.language; }
  }
  return `Track ${track.track + 1}`;
}

function trackDetail(track) {
  const channels = { 1: "mono", 2: "stereo", 6: "5.1", 8: "7.1" }[track.channels] ?? `${track.channels} ch`;
  return `${channels} · ${track.codec.toUpperCase()}`;
}

function setAudioStatus(text) {
  byId("audio-status").textContent = text;
}

function renderAudioButton() {
  const button = byId("audio-button");
  const track = player.info?.audio_tracks?.[player.current];
  button.textContent = track ? `Audio: ${trackName(track)}` : "Audio";
  button.title = track ? `${trackName(track)} (${trackDetail(track)})` : "Audio track";
}

function renderAudioMenu() {
  const menu = byId("audio-menu");
  const info = player.info;
  if (!info) {
    menu.replaceChildren(element("p", { className: "menu-note" }, "Reading the video's audio tracks…"));
    return;
  }
  const items = info.audio_tracks.map((track) => {
    const selected = track.track === player.current && player.pending === null;
    const preparing = track.track === player.pending;
    const item = element("button", {
      type: "button",
      className: "menu-item",
      role: "menuitemradio",
      disabled: !info.can_switch_audio,
    },
    element("span", { className: "check" }, selected ? "✓" : preparing ? "…" : ""),
    element("span", { className: "track-name" }, trackName(track)),
    element("span", { className: "track-detail" }, trackDetail(track)));
    item.setAttribute("aria-checked", String(selected));
    item.addEventListener("click", () => { closeAudioMenu(); selectTrack(track.track); });
    return item;
  });
  if (!info.can_switch_audio) {
    items.push(element("p", { className: "menu-note" }, info.reason || "This video's audio can't be switched"));
  }
  menu.replaceChildren(...items);
}

function openAudioMenu() {
  renderAudioMenu();
  byId("audio-menu").hidden = false;
  byId("audio-button").setAttribute("aria-expanded", "true");
}

function closeAudioMenu() {
  byId("audio-menu").hidden = true;
  byId("audio-button").setAttribute("aria-expanded", "false");
}

byId("audio-button").addEventListener("click", (event) => {
  event.stopPropagation();
  if (byId("audio-menu").hidden) openAudioMenu();
  else closeAudioMenu();
});
document.addEventListener("click", (event) => {
  if (!byId("audio-menu").contains(event.target)) closeAudioMenu();
});

function stopPreparing() {
  player.pollToken++;
  player.pending = null;
}

async function selectTrack(number) {
  const track = player.info?.audio_tracks?.[number];
  if (!track || (number === player.current && player.pending === null)) return;
  stopPreparing();
  if (track.native) {
    detachExternal();
    player.current = number;
    setAudioStatus("");
    renderAudioButton();
    return;
  }
  // Keep playing the current audio until the new track is ready.
  player.pending = number;
  const token = player.pollToken;
  const path = player.path;
  for (;;) {
    let result;
    try {
      result = await api("POST", "/api/v1/media/audio", { path, track: number });
    } catch (error) {
      result = { response: { ok: false, status: 0 }, data: { error: { message: String(error) } } };
    }
    if (token !== player.pollToken) return; // another choice replaced this one
    const { response, data } = result;
    if (!response.ok || data.state === "failed") {
      player.pending = null;
      setAudioStatus(`Couldn't switch to ${trackName(track)}: ${data?.error?.message ?? data?.error ?? `HTTP ${response.status}`}`);
      return;
    }
    if (data.state === "ready") {
      player.pending = null;
      player.current = number;
      attachExternal(data.url);
      setAudioStatus("");
      renderAudioButton();
      return;
    }
    setAudioStatus(`Preparing ${trackName(track)} audio… ${Math.round(data.progress * 100)}% (only the first time)`);
    await new Promise((resolve) => setTimeout(resolve, 1000));
  }
}

// ---- keeping an extracted track in sync with the video --------------------

function attachExternal(url) {
  detachExternal();
  const audio = new Audio();
  audio.preload = "auto";
  audio.src = url;
  audio.playbackRate = video.playbackRate;
  audio.currentTime = video.currentTime;
  audio.addEventListener("waiting", onExternalWaiting);
  audio.addEventListener("canplay", onExternalReady);
  audio.addEventListener("playing", onExternalReady);
  player.external = audio;
  video.muted = true;
  if (!video.paused) audio.play().catch(() => {});
  player.driftTimer = setInterval(correctDrift, 500);
}

function detachExternal() {
  const audio = player.external;
  player.external = null;
  player.resumeAfterAudio = false;
  clearInterval(player.driftTimer);
  video.muted = false;
  if (!audio) return;
  audio.pause();
  audio.removeAttribute("src");
  audio.load();
}

// The audio ran out of data: hold the video until it catches up. (A
// "waiting" event can also come from a quick seek within data the audio
// already has; that needs no pause.)
function onExternalWaiting() {
  const audio = player.external;
  if (audio && !video.paused && audio.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
    player.resumeAfterAudio = true;
    video.pause();
  }
}

function onExternalReady() {
  if (player.resumeAfterAudio) {
    player.resumeAfterAudio = false;
    video.play().catch(() => {});
  }
}

// MAX_DRIFT is how far apart (in seconds) the audio and video may get before
// the audio jumps to the video's position. Smaller gaps are closed gently by
// correctDrift. Jumping on every resume would make the audio stall briefly,
// pause the video, resume it, jump again… in a loop.
const MAX_DRIFT = 0.3;

function syncExternal({ play = false, force = false } = {}) {
  const audio = player.external;
  if (!audio) return;
  if (force || Math.abs(audio.currentTime - video.currentTime) > MAX_DRIFT) {
    audio.currentTime = video.currentTime;
  }
  audio.playbackRate = video.playbackRate;
  if (play) audio.play().catch(() => {});
}

video.addEventListener("play", () => syncExternal({ play: true }));
video.addEventListener("playing", () => syncExternal({ play: true }));
video.addEventListener("seeking", () => syncExternal({ force: true })); // −10, +10, the slider
video.addEventListener("pause", () => {
  if (player.external && !player.resumeAfterAudio) player.external.pause();
});
video.addEventListener("waiting", () => player.external?.pause()); // the video is buffering
video.addEventListener("ended", () => player.external?.pause());
video.addEventListener("ratechange", () => {
  if (player.external) player.external.playbackRate = video.playbackRate;
});

// Small differences are closed gently by playing the audio slightly faster or
// slower; big ones (over 0.3 s) by jumping.
function correctDrift() {
  const audio = player.external;
  if (!audio || video.paused || audio.paused || video.seeking || audio.seeking) return;
  const drift = audio.currentTime - video.currentTime;
  if (Math.abs(drift) > MAX_DRIFT) {
    audio.currentTime = video.currentTime;
    audio.playbackRate = video.playbackRate;
  } else if (Math.abs(drift) > 0.04) {
    // 6% faster or slower closes 0.1 s in under 2 s without sounding odd.
    const nudge = Math.abs(drift) > 0.1 ? 0.06 : 0.03;
    audio.playbackRate = video.playbackRate * (drift > 0 ? 1 - nudge : 1 + nudge);
  } else {
    audio.playbackRate = video.playbackRate;
  }
}
