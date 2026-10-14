// Where the app is lives in the address after #, so the phone's Back button
// works and every screen can be bookmarked:
//   #/Photos/2024              a folder
//   #play=/Videos/trip.mov     the video player
//   #view=/Photos/beach.jpg    the photo viewer
//   #share                     files shared from another app

import { encodePath } from "./paths";
import { parseRoute, type Route } from "./route";

export type { Route };

export const router = $state({ route: parseRoute(location.hash) });

// Whether there's an earlier screen of the app to go back to, rather than
// whatever was open before the app.
let movedWithinApp = false;

window.addEventListener("hashchange", () => {
  movedWithinApp = true;
  router.route = parseRoute(location.hash);
});

export const link = {
  folder: (path: string) => "#" + encodePath(path),
  player: (path: string) => "#play=" + encodePath(path),
  viewer: (path: string) => "#view=" + encodePath(path),
};

export function go(hash: string, { replace = false } = {}) {
  if (replace) {
    history.replaceState(null, "", hash);
    router.route = parseRoute(hash);
  } else {
    location.hash = hash;
  }
}

/** The folder on screen, or the folder of the file on screen. */
export function currentFolder(): string {
  const route = router.route;
  if (route.view === "folder") return route.path;
  if (route.view === "share") return "/";
  return route.path.slice(0, route.path.lastIndexOf("/")) || "/";
}

/** Back to the previous screen, like the phone's Back button, or to
 * fallback if the app was opened straight onto this one. */
export function goBack(fallback: string) {
  if (movedWithinApp) history.back();
  else go(fallback, { replace: true });
}
