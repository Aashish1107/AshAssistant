import type { Plugin } from "vite";
import { defineConfig } from "vitest/config";
import { svelte } from "@sveltejs/vite-plugin-svelte";
import { createHash } from "node:crypto";
import { readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { join, relative, sep } from "node:path";
import { brotliCompressSync, constants, gzipSync } from "node:zlib";

// The Go server embeds this folder (see server/internal/webui).
const outDir = "../server/internal/webui/dist";

function filesIn(dir: string): string[] {
  return readdirSync(dir).flatMap((name) => {
    const path = join(dir, name);
    return statSync(path).isDirectory() ? filesIn(path) : [path];
  });
}

// afterBuild finishes the build in two steps:
//
//  1. sw.js: the service worker, with the list of every built file filled in,
//     so the installed app opens from the phone's storage without waiting for
//     the laptop. Its version changes whenever any file does, which is how
//     phones learn there's an update.
//  2. Pre-compressed copies (.br, .gz) of every text file, made once here at
//     the highest compression instead of on every request.
function afterBuild(): Plugin {
  return {
    name: "ashcloud-after-build",
    apply: "build",
    closeBundle() {
      const files = filesIn(outDir)
        .map((path) => "/" + relative(outDir, path).split(sep).join("/"))
        .filter((url) => !/\.(br|gz)$/.test(url) && url !== "/sw.js")
        .sort();
      const urls = files.map((url) => (url === "/index.html" ? "/" : url));
      const hash = createHash("sha256");
      for (const url of files) hash.update(url).update(readFileSync(join(outDir, url)));
      const version = hash.digest("hex").slice(0, 12);
      const worker = readFileSync("src/sw.js", "utf8")
        .replace('"%VERSION%"', JSON.stringify(version))
        .replace("[] /* %FILES% */", JSON.stringify(urls));
      writeFileSync(join(outDir, "sw.js"), worker);

      for (const path of filesIn(outDir)) {
        if (!/\.(js|css|html|svg|webmanifest|json|txt)$/.test(path)) continue;
        const body = readFileSync(path);
        if (body.length < 1024) continue;
        writeFileSync(path + ".br", brotliCompressSync(body, { params: { [constants.BROTLI_PARAM_QUALITY]: 11 } }));
        writeFileSync(path + ".gz", gzipSync(body, { level: 9 }));
      }
    },
  };
}

export default defineConfig({
  plugins: [svelte(), afterBuild()],
  build: {
    outDir,
    emptyOutDir: true,
    assetsDir: "assets",
    // Small files are inlined into the bundle rather than fetched separately.
    assetsInlineLimit: 4096,
    reportCompressedSize: true,
  },
  server: {
    // npm run dev: the app on http://localhost:5173, talking to the Go server.
    proxy: {
      "/api": "http://127.0.0.1:8080",
    },
  },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
