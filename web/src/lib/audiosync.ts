// Playing a video with one of its other audio tracks.
//
// Chrome on Android only plays a video's first audio track. To hear another
// one, the server extracts that track to its own file; the video is muted and
// the extracted track plays alongside it, kept in sync through play, pause,
// skips, scrubbing and buffering.

// MAX_DRIFT is how far apart (in seconds) the audio and video may get before
// the audio jumps to the video's position. Smaller gaps are closed gently by
// playing the audio slightly faster or slower. Jumping on every resume would
// make the audio stall briefly, pause the video, resume it, jump again… in a
// loop.
const MAX_DRIFT = 0.3;

export class AudioSync {
  private audio: HTMLAudioElement | null = null;
  private timer = 0;
  private resumeAfterAudio = false; // the video was paused to let the audio catch up
  private readonly listeners: [string, () => void][];

  constructor(private readonly video: HTMLVideoElement) {
    this.listeners = [
      ["play", () => this.sync({ play: true })],
      ["playing", () => this.sync({ play: true })],
      ["seeking", () => this.sync({ force: true })], // −10, +10, the slider
      ["pause", () => {
        if (this.audio && !this.resumeAfterAudio) this.audio.pause();
      }],
      ["waiting", () => this.audio?.pause()], // the video is buffering
      ["ended", () => this.audio?.pause()],
      ["ratechange", () => {
        if (this.audio) this.audio.playbackRate = video.playbackRate;
      }],
    ];
    for (const [name, listener] of this.listeners) video.addEventListener(name, listener);
  }

  /** Plays the extracted track at url instead of the video's own sound. */
  attach(url: string) {
    this.detach();
    const audio = new Audio();
    audio.preload = "auto";
    audio.src = url;
    audio.playbackRate = this.video.playbackRate;
    audio.currentTime = this.video.currentTime;
    audio.addEventListener("waiting", this.onAudioWaiting);
    audio.addEventListener("canplay", this.onAudioReady);
    audio.addEventListener("playing", this.onAudioReady);
    // Kept next to the video (hidden) so it's easy to find when debugging.
    audio.hidden = true;
    audio.className = "extra-audio";
    this.video.after(audio);
    this.audio = audio;
    this.video.muted = true;
    if (!this.video.paused) audio.play().catch(() => {});
    this.timer = window.setInterval(this.correctDrift, 500);
  }

  /** Back to the video's own sound. */
  detach() {
    const audio = this.audio;
    this.audio = null;
    this.resumeAfterAudio = false;
    clearInterval(this.timer);
    this.video.muted = false;
    if (!audio) return;
    audio.pause();
    audio.removeAttribute("src");
    audio.load();
    audio.remove();
  }

  destroy() {
    this.detach();
    for (const [name, listener] of this.listeners) this.video.removeEventListener(name, listener);
  }

  private sync({ play = false, force = false } = {}) {
    const audio = this.audio;
    if (!audio) return;
    if (force || Math.abs(audio.currentTime - this.video.currentTime) > MAX_DRIFT) {
      audio.currentTime = this.video.currentTime;
    }
    audio.playbackRate = this.video.playbackRate;
    if (play) audio.play().catch(() => {});
  }

  // The audio ran out of data: hold the video until it catches up. (A
  // "waiting" event can also come from a quick seek within data the audio
  // already has; that needs no pause.)
  private onAudioWaiting = () => {
    const audio = this.audio;
    if (audio && !this.video.paused && audio.readyState < HTMLMediaElement.HAVE_FUTURE_DATA) {
      this.resumeAfterAudio = true;
      this.video.pause();
    }
  };

  private onAudioReady = () => {
    if (this.resumeAfterAudio) {
      this.resumeAfterAudio = false;
      this.video.play().catch(() => {});
    }
  };

  // Small differences are closed gently by playing the audio slightly faster
  // or slower; big ones (over MAX_DRIFT) by jumping.
  private correctDrift = () => {
    const audio = this.audio;
    const video = this.video;
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
  };
}
