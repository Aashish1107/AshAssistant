// Ash Cloud's service worker. The build fills in VERSION and FILES.
//
// It does two things:
//  - The app's own files (page, script, styles, icons) are kept on the phone,
//    so the installed app opens instantly, even before the laptop answers.
//    Your files and everything under /api/ always come from the laptop.
//  - Android's Share menu can send photos and videos to Ash Cloud: the shared
//    files are handed to the page, which uploads them.

const VERSION = "%VERSION%";
const FILES = [] /* %FILES% */;
const CACHE = "ashcloud-" + VERSION;

self.addEventListener("install", (event) => {
  event.waitUntil(
    caches.open(CACHE)
      .then((cache) => cache.addAll(FILES))
      .then(() => self.skipWaiting()),
  );
});

self.addEventListener("activate", (event) => {
  event.waitUntil((async () => {
    for (const key of await caches.keys()) {
      if (key !== CACHE) await caches.delete(key);
    }
    await self.clients.claim();
  })());
});

self.addEventListener("fetch", (event) => {
  const request = event.request;
  const url = new URL(request.url);
  if (url.origin !== self.location.origin) return;

  if (request.method === "POST" && url.pathname === "/share") {
    event.respondWith(receiveShare(event));
    return;
  }
  if (request.method !== "GET") return;
  // Opening the app: the page from the phone's storage. (Other pages, such as
  // a PDF opened from the app, come from the laptop as usual.)
  const key = url.pathname === "/" || url.pathname === "/index.html" ? "/" : url.pathname;
  if (!FILES.includes(key) || (key === "/" && url.search)) return;
  event.respondWith(
    caches.match(key).then((hit) => hit || fetch(request)),
  );
});

// ---- sharing from other apps --------------------------------------------------

let shared = null; // { files, done } until the page picks the files up

async function receiveShare(event) {
  const form = await event.request.formData();
  const files = form.getAll("files").filter((file) => typeof file === "object");
  // Keep this worker alive until the page has the files (or 2 minutes pass).
  event.waitUntil(new Promise((done) => {
    shared = { files, done };
    setTimeout(done, 120000);
  }));
  return Response.redirect("/#share", 303);
}

self.addEventListener("message", (event) => {
  if (event.data !== "shared-files") return;
  event.source.postMessage({ type: "shared-files", files: shared ? shared.files : [] });
  if (shared) shared.done();
  shared = null;
});
