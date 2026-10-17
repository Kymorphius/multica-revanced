import "./env";

import { expect, test, type TestInfo } from "@playwright/test";
import pg from "pg";
import { TestApiClient } from "./fixtures";

function requireLocalFixtures(testInfo: TestInfo): string {
  const databaseUrl = process.env.DATABASE_URL;
  const frontendUrl = testInfo.project.use.baseURL;
  if (!databaseUrl || typeof frontendUrl !== "string") {
    throw new Error(
      "Shared memory E2E requires explicit local database and browser targets",
    );
  }
  const apiUrl =
    process.env.NEXT_PUBLIC_API_URL ||
    `http://localhost:${process.env.PORT || "8080"}`;
  for (const value of [databaseUrl, frontendUrl, apiUrl]) {
    if (
      !["localhost", "127.0.0.1", "[::1]"].includes(new URL(value).hostname)
    ) {
      throw new Error(
        "Shared memory E2E only runs against local development services",
      );
    }
  }
  return databaseUrl;
}

test("shared memories persist workspace/project CRUD, search, and automatic saving", async ({
  page,
}, testInfo) => {
  test.setTimeout(180_000);
  const databaseUrl = requireLocalFixtures(testInfo);
  const api = new TestApiClient();
  const db = new pg.Client(databaseUrl);
  const runId = `${Date.now().toString(36)}-${process.pid.toString(36)}`;
  await db.connect();
  try {
    await api.login(
      `e2e-memories-${runId}@multica.ai`,
      "Memory Browser Fixture",
    );
    const workspace = await api.ensureWorkspace(
      "Memory Browser Fixture",
      `e2e-memories-${runId}`,
    );
    await api.markUserOnboarded();
    const project = await api.createProject("Memory scope project");
    const token = api.getToken();
    if (!token)
      throw new Error("Memory browser fixture login did not return a token");
    await page.addInitScript((value) => {
      localStorage.setItem("multica_token", value);
      localStorage.setItem("multica:chat:isOpen", "false");
    }, token);
    await page.setViewportSize({ width: 1440, height: 1100 });
    await page.goto(`/${workspace.slug}/memories`, {
      waitUntil: "domcontentloaded",
    });
    await expect(
      page.getByRole("heading", { name: "Shared memories", exact: true }),
    ).toBeVisible({ timeout: 60_000 });
    await expect(
      page.getByText(/All saved memories are visible to workspace members/),
    ).toBeVisible();
    const automatic = page.getByRole("switch", {
      name: "Automatically save task experience",
    });
    await expect(automatic).toBeEnabled();
    await expect(automatic).not.toBeChecked();

    await page
      .getByRole("button", { name: "Save memory", exact: true })
      .click();
    const editor = page.getByRole("dialog");
    await editor
      .getByLabel("Title", { exact: true })
      .fill("Workspace checking preference");
    await editor
      .getByLabel("Content", { exact: true })
      .fill("Run focused checks before committing.\n保留已有改动。");
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect(editor).toBeHidden();
    const workspaceCard = page
      .locator("article")
      .filter({
        has: page.getByRole("heading", {
          name: "Workspace checking preference",
          exact: true,
        }),
      });
    await expect(workspaceCard).toContainText("Whole workspace");
    await expect(workspaceCard).toContainText("Manually saved");
    const stored = await db.query<{
      id: string;
      project_id: string | null;
      content: string;
      source: string;
      revision: number;
    }>(
      "SELECT id, project_id, content, source, revision FROM memory WHERE workspace_id = $1 AND title = $2",
      [workspace.id, "Workspace checking preference"],
    );
    expect(stored.rows).toEqual([
      {
        id: expect.any(String),
        project_id: null,
        content: "Run focused checks before committing.\n保留已有改动。",
        source: "manual",
        revision: 1,
      },
    ]);
    const memoryId = stored.rows[0]!.id;

    await page
      .getByRole("button", { name: "Save memory", exact: true })
      .click();
    await editor
      .getByLabel("Title", { exact: true })
      .fill("Project build knowledge");
    await editor
      .getByLabel("Content", { exact: true })
      .fill("Use separator checks for this project.");
    await editor
      .getByLabel("Task scope", { exact: true })
      .selectOption(project.id);
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect(editor).toBeHidden();
    const projectCard = page
      .locator("article")
      .filter({
        has: page.getByRole("heading", {
          name: "Project build knowledge",
          exact: true,
        }),
      });
    await expect(projectCard).toContainText("Project: Memory scope project");
    const projectMemory = await db.query<{ project_id: string }>(
      "SELECT project_id FROM memory WHERE workspace_id = $1 AND title = $2",
      [workspace.id, "Project build knowledge"],
    );
    expect(projectMemory.rows).toEqual([{ project_id: project.id }]);

    const search = page.getByRole("textbox", {
      name: "Search memories",
      exact: true,
    });
    await search.fill("separator checks");
    await expect(projectCard).toBeVisible();
    await expect(workspaceCard).toHaveCount(0);
    await search.fill("not-present-in-either-memory");
    await expect(
      page.getByText("No matching memories", { exact: true }),
    ).toBeVisible();
    await search.fill("");
    await expect(workspaceCard).toBeVisible();

    await workspaceCard
      .getByRole("button", {
        name: "Edit Workspace checking preference",
        exact: true,
      })
      .click();
    await expect(
      editor.getByLabel("Task scope", { exact: true }),
    ).toBeDisabled();
    await editor
      .getByLabel("Content", { exact: true })
      .fill(
        "Run focused checks, then save the reviewed changes.\n保留已有改动。",
      );
    await editor.getByRole("button", { name: "Save", exact: true }).click();
    await expect(editor).toBeHidden();
    await expect(workspaceCard).toContainText("then save the reviewed changes");
    const updated = await db.query<{ content: string; revision: number }>(
      "SELECT content, revision FROM memory WHERE workspace_id = $1 AND id = $2",
      [workspace.id, memoryId],
    );
    expect(updated.rows).toEqual([
      {
        content:
          "Run focused checks, then save the reviewed changes.\n保留已有改动。",
        revision: 2,
      },
    ]);

    await automatic.click();
    await expect(automatic).toBeChecked();
    const enabled = await db.query<{ automatic_extraction: boolean }>(
      "SELECT automatic_extraction FROM workspace_memory_settings WHERE workspace_id = $1",
      [workspace.id],
    );
    expect(enabled.rows).toEqual([{ automatic_extraction: true }]);
    await page.reload({ waitUntil: "domcontentloaded" });
    await expect(automatic).toBeChecked({ timeout: 30_000 });
    await expect(workspaceCard).toContainText("then save the reviewed changes");
    await expect(projectCard).toContainText("Project: Memory scope project");
    await page.screenshot({
      path: testInfo.outputPath("shared-memories.png"),
      fullPage: true,
    });
    await automatic.click();
    await expect(automatic).not.toBeChecked();
    const disabled = await db.query<{ automatic_extraction: boolean }>(
      "SELECT automatic_extraction FROM workspace_memory_settings WHERE workspace_id = $1",
      [workspace.id],
    );
    expect(disabled.rows).toEqual([{ automatic_extraction: false }]);

    await workspaceCard
      .getByRole("button", {
        name: "Delete Workspace checking preference",
        exact: true,
      })
      .click();
    const confirmation = page.getByRole("alertdialog");
    await expect(confirmation).toContainText(
      "will no longer be available to workspace members or agents",
    );
    await confirmation
      .getByRole("button", { name: "Cancel", exact: true })
      .click();
    await expect(workspaceCard).toBeVisible();
    await workspaceCard
      .getByRole("button", {
        name: "Delete Workspace checking preference",
        exact: true,
      })
      .click();
    await confirmation
      .getByRole("button", { name: "Delete", exact: true })
      .click();
    await expect(confirmation).toBeHidden();
    await expect(workspaceCard).toHaveCount(0);
    await projectCard
      .getByRole("button", {
        name: "Delete Project build knowledge",
        exact: true,
      })
      .click();
    await confirmation
      .getByRole("button", { name: "Delete", exact: true })
      .click();
    await expect(confirmation).toBeHidden();
    await expect(
      page.getByText("No saved memories", { exact: true }),
    ).toBeVisible();
    const remaining = await db.query<{ count: number }>(
      "SELECT count(*)::int AS count FROM memory WHERE workspace_id = $1",
      [workspace.id],
    );
    expect(remaining.rows).toEqual([{ count: 0 }]);
  } finally {
    try {
      await api.cleanup();
      await api.deleteWorkspace();
    } finally {
      await db.end();
    }
  }
});
