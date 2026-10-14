// Talking to the Ash Cloud server. Every request that changes something
// carries the X-Requested-With header the server's CSRF check requires.

export interface User {
  id: string;
  username: string;
  role: "admin" | "user";
}

export interface Session {
  user: User;
  expires_at: string;
}

export interface Entry {
  name: string;
  path: string;
  type: "folder" | "file";
  size?: number;
  modified: string;
  content_type?: string;
}

export interface Listing {
  path: string;
  entries: Entry[];
}

export interface UploadStatus {
  id: string;
  path: string;
  size: number;
  offset: number;
  file?: Entry;
}

export interface AudioTrack {
  track: number;
  codec: string;
  channels: number;
  language?: string;
  title?: string;
  default?: boolean;
  native?: boolean;
}

export interface MediaInfo {
  duration_seconds?: number;
  audio_tracks: AudioTrack[];
  can_switch_audio: boolean;
  reason?: string;
}

export interface AudioPrepare {
  state: "preparing" | "ready" | "failed";
  progress?: number;
  url?: string;
  error?: string;
}

/** The server answered with an error. */
export class ApiError extends Error {
  constructor(
    readonly status: number,
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

/** The server couldn't be reached at all. */
export class NetworkError extends Error {
  constructor() {
    super("Can't reach the laptop. Check that the Tailscale app is on and the server is running.");
  }
}

// Called when the server says the session has ended, so the app can show
// the login screen.
let onLoggedOut = () => {};
export function whenLoggedOut(callback: () => void) {
  onLoggedOut = callback;
}

export async function api<T>(method: string, path: string, body?: unknown): Promise<T> {
  let response: Response;
  try {
    response = await fetch(path, {
      method,
      headers: {
        "X-Requested-With": "ashcloud",
        ...(body === undefined ? {} : { "Content-Type": "application/json" }),
      },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new NetworkError();
  }
  if (response.status === 204) return undefined as T;
  let data: any = null;
  try {
    data = await response.json();
  } catch {
    // not JSON (e.g. a proxy's error page)
  }
  if (!response.ok) {
    const error = new ApiError(
      response.status,
      data?.error?.code ?? "http_" + response.status,
      data?.error?.message ?? `Something went wrong (HTTP ${response.status})`,
    );
    if (response.status === 401 && !path.endsWith("/auth/login")) onLoggedOut();
    throw error;
  }
  return data as T;
}

/** A message for the user from any error. */
export function messageOf(error: unknown): string {
  if (error instanceof ApiError || error instanceof NetworkError) return error.message;
  return String(error);
}
