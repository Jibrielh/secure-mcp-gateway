import { expect, test, type Page } from "@playwright/test";

async function signIn(page: Page, username: string) {
  await page.goto("/");
  await page.getByLabel("Username").fill(username);
  await page.getByLabel("Password").fill("dev-only-password-value");
  await page.getByRole("button", { name: "Sign in" }).click();
  await expect(page.getByRole("link", { name: "Chat" })).toBeVisible();
}

test("chat uses the gateway for an allowed utility call", async ({ page }) => {
  await page.goto("/");
  await page.getByLabel("Username").fill("ada");
  await page.getByLabel("Password").fill("dev-only-password-value");
  await page.getByRole("button", { name: "Sign in" }).click();
  await page.getByRole("link", { name: "Chat" }).click();
  await page.getByLabel("Agent").selectOption({ label: "Workspace Assistant" });
  await page.getByRole("button", { name: "New conversation" }).first().click();
  await page.getByLabel("Message").fill("Add 22 and 35");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("Using the gateway result: 57")).toBeVisible({ timeout: 20_000 });
});

test("a different operator approves a high-risk tool and the requester resumes it", async ({ page, browser }) => {
  await signIn(page, "ada");
  await page.getByRole("link", { name: "Agents" }).click();
  await page.getByLabel("Name").fill("Maintenance");
  await page.getByLabel("Instructions").fill("Use the maintenance tool when asked.");
  await page.getByRole("checkbox", { name: "admin.mock_destructive_action" }).check();
  await page.getByRole("button", { name: "Create" }).click();
  await expect(page.getByRole("heading", { name: "Maintenance" })).toBeVisible();
  await page.getByRole("link", { name: "Chat" }).click();
  await page.getByLabel("Agent").selectOption({ label: "Maintenance" });
  await page.getByRole("button", { name: "New conversation" }).first().click();
  await page.getByLabel("Message").fill("Run maintenance");
  await page.getByRole("button", { name: "Send" }).click();
  await expect(page.getByText("Waiting for a different operator")).toBeVisible();

  const owenContext = await browser.newContext();
  const owen = await owenContext.newPage();
  await signIn(owen, "owen");
  await owen.getByRole("link", { name: "Approvals" }).click();
  await owen.getByRole("button", { name: "Approve exact action" }).click();
  await expect(owen.getByText("approved").first()).toBeVisible();
  await owenContext.close();

  await page.getByRole("button", { name: "Resume approved action" }).click();
  await expect(page.getByText("Mock maintenance completed")).toBeVisible({ timeout: 20_000 });
});
