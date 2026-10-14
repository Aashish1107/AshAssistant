import { expect, test, type Page } from "@playwright/test";

const username = "tester";
const password = "end to end password";

async function logIn(page: Page, path = "/") {
  await page.goto(path);
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.locator(".list, .empty").first()).toBeVisible();
}

function row(page: Page, name: string) {
  return page.locator(".row", { has: page.locator(".name", { hasText: new RegExp(`^${name.replace(/[.()]/g, "\\$&")}$`) }) });
}

// Every test watches for script errors and Content-Security-Policy violations.
test.beforeEach(async ({ page }) => {
  const problems: string[] = [];
  page.on("pageerror", (error) => problems.push(error.message));
  page.on("console", (message) => {
    if (message.type() === "error" && !/status of 401|ERR_INTERNET_DISCONNECTED/.test(message.text())) problems.push(message.text());
  });
  (page as any).problems = problems;
});

test.afterEach(async ({ page }) => {
  expect((page as any).problems as string[]).toEqual([]);
});

test("logging in", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password").fill("not the password");
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(page.getByRole("alert")).toHaveText("Wrong username or password");
  await page.getByLabel("Password").fill(password);
  await page.getByRole("button", { name: "Log in" }).click();
  await expect(row(page, "Photos")).toBeVisible();
  await expect(page.getByRole("button", { name: "Account: tester" })).toBeVisible();
});

test("upload, rename, move, delete and undo", async ({ page }) => {
  await logIn(page);

  await page.getByRole("button", { name: "New folder" }).click();
  await page.getByRole("dialog").getByRole("textbox").fill("Trip");
  await page.getByRole("button", { name: "Create" }).click();
  await row(page, "Trip").locator("a.open").click();
  await expect(page.locator("h1")).toHaveText("Trip");

  await page.locator("input[type=file]").setInputFiles([
    { name: "IMG_0001.jpg", mimeType: "image/jpeg", buffer: Buffer.alloc(300_000, 1) },
    { name: "Clip.mp4", mimeType: "video/mp4", buffer: Buffer.alloc(9_000_000, 2) },
  ]);
  await expect(page.locator("button.bar")).toContainText("All 2 uploaded");
  await expect(row(page, "IMG_0001.jpg")).toContainText("293 KB");
  await expect(row(page, "Clip.mp4")).toContainText("8.6 MB");

  // Rename: the name without its extension is selected, ready to type over.
  await row(page, "IMG_0001.jpg").getByRole("button", { name: /More for/ }).click();
  await page.getByRole("button", { name: "Rename" }).click();
  const input = page.getByRole("dialog").getByRole("textbox");
  expect(await input.evaluate((el: HTMLInputElement) => el.value.slice(el.selectionStart!, el.selectionEnd!))).toBe("IMG_0001");
  await input.fill("Sunrise.jpg");
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await expect(row(page, "Sunrise.jpg")).toBeVisible();

  // A name Windows can't use is refused before it reaches the laptop.
  await row(page, "Sunrise.jpg").getByRole("button", { name: /More for/ }).click();
  await page.getByRole("button", { name: "Rename" }).click();
  await page.getByRole("dialog").getByRole("textbox").fill("Sun: rise.jpg");
  await page.getByRole("button", { name: "Rename", exact: true }).click();
  await expect(page.getByRole("dialog").getByRole("alert")).toContainText("can't contain");
  await page.getByRole("button", { name: "Cancel" }).click();

  // Move into another folder.
  await row(page, "Clip.mp4").getByRole("button", { name: /More for/ }).click();
  await page.getByRole("button", { name: "Move to…" }).click();
  await page.getByRole("button", { name: "Home" }).click();
  await page.locator(".folder", { hasText: "Documents" }).click();
  await page.getByRole("button", { name: "Move to Documents" }).click();
  await expect(page.locator(".toast")).toContainText("Moved to Documents");
  await expect(row(page, "Clip.mp4")).toHaveCount(0);

  // Delete is instant, with Undo.
  await row(page, "Sunrise.jpg").getByRole("button", { name: /More for/ }).click();
  await page.getByRole("button", { name: "Delete" }).click();
  await expect(row(page, "Sunrise.jpg")).toHaveCount(0);
  await page.locator(".toast").getByRole("button", { name: "Undo" }).click();
  await expect(row(page, "Sunrise.jpg")).toBeVisible();

  await page.goto("/#/Documents");
  await expect(row(page, "Clip.mp4")).toBeVisible();
});

test("an upload that's cancelled leaves nothing behind", async ({ page }) => {
  await logIn(page, "/#/Documents");
  // Slow the phone's connection down so there's time to cancel.
  const cdp = await page.context().newCDPSession(page);
  await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 50, downloadThroughput: 1e6, uploadThroughput: 1e6 });
  await page.locator("input[type=file]").setInputFiles([{ name: "Big.bin", mimeType: "application/octet-stream", buffer: Buffer.alloc(40_000_000, 3) }]);
  await page.locator("button.bar").click();
  await page.getByRole("button", { name: "Cancel Big.bin" }).click();
  await expect(page.locator(".upload")).toHaveCount(0);
  await cdp.send("Network.emulateNetworkConditions", { offline: false, latency: 0, downloadThroughput: -1, uploadThroughput: -1 });
  await page.keyboard.press("Escape");
  await page.reload();
  await expect(row(page, "notes.txt")).toBeVisible();
  await expect(row(page, "Big.bin")).toHaveCount(0);
});

test("photo viewer: swipe, and Back closes it", async ({ page }) => {
  await logIn(page, "/#/Photos/Beach");
  await row(page, "sand.png").locator("a.open").click();
  const viewer = page.getByRole("dialog", { name: "Photo: sand.png" });
  await expect(viewer.locator("img.loaded")).toBeVisible();
  await expect(viewer).toContainText("1 of 3");

  const stage = await viewer.locator(".stage").boundingBox();
  await page.mouse.move(stage!.x + stage!.width * 0.8, stage!.y + stage!.height / 2);
  await page.mouse.down();
  await page.mouse.move(stage!.x + stage!.width * 0.2, stage!.y + stage!.height / 2, { steps: 5 });
  await page.mouse.up();
  await expect(page.getByRole("dialog", { name: "Photo: sunset.png" })).toContainText("2 of 3");

  await page.goBack();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await expect(page.locator("h1")).toHaveText("Beach");
});

test("installable, opens with no connection, and takes files shared from other apps", async ({ page, context }) => {
  await logIn(page);
  await page.waitForFunction(() => navigator.serviceWorker.controller !== null);

  const cdp = await context.newCDPSession(page);
  expect((await cdp.send("Page.getAppManifest")).errors).toEqual([]);
  expect((await cdp.send("Page.getInstallabilityErrors")).installabilityErrors).toEqual([]);

  // What Android's Share menu does when Ash Cloud is picked.
  const answer = await page.evaluate(async () => {
    const form = new FormData();
    form.append("files", new File(["shared"], "From gallery.jpg", { type: "image/jpeg" }));
    return (await fetch("/share", { method: "POST", body: form, redirect: "manual" })).type;
  });
  expect(answer).toBe("opaqueredirect");
  await page.goto("/#share");
  await expect(page.locator("h1")).toHaveText("Upload 1 file");
  await page.locator(".folder", { hasText: "Photos" }).click();
  await page.getByRole("button", { name: "Upload to Photos" }).click();
  await expect(row(page, "From gallery.jpg")).toBeVisible();

  // The app itself comes from the phone's storage.
  await context.setOffline(true);
  await page.goto("/");
  await expect(page.getByText("Can't reach the laptop.")).toBeVisible();
  await context.setOffline(false);
  await page.getByRole("button", { name: "Try again" }).click();
  await expect(row(page, "Photos")).toBeVisible();
});
