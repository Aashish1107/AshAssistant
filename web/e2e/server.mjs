// Starts a throwaway Ash Cloud server for the end-to-end tests: builds the
// Go server, sets it up in a temporary folder with one admin account and a
// few files, and runs it until Playwright stops it.
import { execFileSync, spawn } from "node:child_process";
import { mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";

export const port = 8097;
export const username = "tester";
export const password = "end to end password";

const dir = mkdtempSync(join(tmpdir(), "ashcloud-e2e-"));
const binary = join(dir, process.platform === "win32" ? "ashcloud.exe" : "ashcloud");
const config = join(dir, "data", "ashcloud.toml");

execFileSync("go", ["build", "-o", binary, "./cmd/ashcloud"], { cwd: "../server", stdio: "inherit" });
execFileSync(binary, ["init", "--data-dir", join(dir, "data")], { stdio: "ignore" });
execFileSync(binary, ["user", "create", "--admin", "--config", config, username], { input: `${password}\n${password}\n`, stdio: ["pipe", "ignore", "inherit"] });

// A 2×2 PNG: something the photo viewer can really show.
const png = Buffer.from(
  "iVBORw0KGgoAAAANSUhEUgAAAAIAAAACCAIAAAD91JpzAAAAFklEQVR4nGP4z8DAwMDAxMDAwMDAAAANHQEDasKb6QAAAABJRU5ErkJggg==",
  "base64",
);
const home = join(dir, "data", "storage", "users", "1");
for (const [path, body] of [
  ["Photos/Beach/sunset.png", png],
  ["Photos/Beach/waves.png", png],
  ["Photos/Beach/sand.png", png],
  ["Documents/notes.txt", "Shopping list\n"],
]) {
  mkdirSync(join(home, path, ".."), { recursive: true });
  writeFileSync(join(home, path), body);
}

// The server's log is shown only when asked for: ASHCLOUD_LOGS=1 npm run e2e
const logs = process.env.ASHCLOUD_LOGS ? "inherit" : "ignore";
const server = spawn(binary, ["serve", "--config", config, "--listen", `127.0.0.1:${port}`], { stdio: ["ignore", logs, logs] });
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => server.kill(signal));
server.on("exit", (code) => process.exit(code ?? 0));
