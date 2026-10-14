// Who's logged in on this device.

import { api, NetworkError, type Session } from "./api";

export const session = $state({
  current: null as Session | null,
  checked: false, // whether the server has been asked yet
  offline: false, // the server couldn't be reached to ask
});

export async function checkSession() {
  try {
    session.current = await api<Session>("GET", "/api/v1/me");
    session.offline = false;
  } catch (error) {
    session.current = null;
    session.offline = error instanceof NetworkError;
  } finally {
    session.checked = true;
  }
}

export async function logIn(username: string, password: string) {
  session.current = await api<Session>("POST", "/api/v1/auth/login", { username, password });
  session.offline = false;
}

export async function logOut() {
  await api("POST", "/api/v1/auth/logout");
  session.current = null;
}
