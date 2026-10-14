// App-wide things: the service worker, installing to the home screen, and
// files shared from other apps.

export const app = $state({
  installable: false, // the browser offers "Add to home screen"
  installed: matchMedia("(display-mode: standalone)").matches,
});

let installPrompt: any = null;

window.addEventListener("beforeinstallprompt", (event) => {
  event.preventDefault(); // offered from the account menu instead
  installPrompt = event;
  app.installable = true;
});

window.addEventListener("appinstalled", () => {
  installPrompt = null;
  app.installable = false;
  app.installed = true;
});

export async function install() {
  if (!installPrompt) return;
  installPrompt.prompt();
  await installPrompt.userChoice.catch(() => null);
  installPrompt = null;
  app.installable = false;
}

export function registerServiceWorker() {
  if (!("serviceWorker" in navigator) || import.meta.env.DEV) return;
  navigator.serviceWorker.register("/sw.js").catch(() => {
    // Not over HTTPS (or blocked): the app still works, just not offline.
  });
}

/** Files shared to Ash Cloud from another app (Android's Share menu). */
export function sharedFiles(): Promise<File[]> {
  return new Promise((resolve) => {
    const worker = navigator.serviceWorker?.controller;
    if (!worker) return resolve([]);
    const timeout = setTimeout(() => resolve([]), 5000);
    const listener = (event: MessageEvent) => {
      if (event.data?.type !== "shared-files") return;
      clearTimeout(timeout);
      navigator.serviceWorker.removeEventListener("message", listener);
      resolve(event.data.files ?? []);
    };
    navigator.serviceWorker.addEventListener("message", listener);
    worker.postMessage("shared-files");
  });
}
