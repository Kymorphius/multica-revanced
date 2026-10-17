// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import { MemorySettingsSchema, SharedMemorySchema } from "./schemas";

afterEach(() => vi.unstubAllGlobals());
const wireMemory = {
  id: "m1",
  workspace_id: "w1",
  project_id: null,
  title: "Build",
  content: "Run focused checks",
  source: "manual",
  revision: 4,
};
function mockResponse(value: unknown) {
  const fetchMock = vi
    .fn()
    .mockImplementation(() =>
      Promise.resolve(
        new Response(JSON.stringify(value), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );
  vi.stubGlobal("fetch", fetchMock);
  return fetchMock;
}

describe("shared memory boundary", () => {
  it("keeps content and revision, safely defaults future sources and malformed optional fields", () => {
    expect(
      SharedMemorySchema.parse({
        ...wireMemory,
        source: "future",
        project_id: 3,
        created_at: false,
      }),
    ).toMatchObject({
      id: "m1",
      workspaceId: "w1",
      projectId: null,
      content: "Run focused checks",
      source: "unknown",
      revision: 4,
      createdAt: "",
    });
    expect(MemorySettingsSchema.parse({ automatic_extraction: "yes" })).toEqual(
      { automaticExtraction: false },
    );
  });
  it("bounds and encodes list requests and tolerates malformed responses", async () => {
    const fetchMock = mockResponse({ unexpected: true });
    const client = new ApiClient("https://api.example.test");
    expect(
      await client.listMemories({ q: "A & B", projectId: "p1", offset: 100 }),
    ).toEqual([]);
    const url = String(fetchMock.mock.calls[0]?.[0]);
    expect(url).toContain("q=A+%26+B");
    expect(url).toContain("project_id=p1");
    expect(url).toContain("limit=100&offset=100");
  });
  it("sends explicit manual source and CAS baselines on edits and deletion", async () => {
    const fetchMock = mockResponse(wireMemory);
    const client = new ApiClient("https://api.example.test");
    expect(
      await client.createMemory({
        title: "Build",
        content: "Run focused checks",
        projectId: "p1",
      }),
    ).toMatchObject({ workspaceId: "w1", revision: 4 });
    await client.updateMemory("m1", {
      title: "Update",
      content: "Updated",
      expectedRevision: 4,
    });
    await client.deleteMemory("m1", 5);
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1].body)).toEqual({
      title: "Build",
      content: "Run focused checks",
      project_id: "p1",
      source: "manual",
    });
    expect(JSON.parse(fetchMock.mock.calls[1]?.[1].body)).toEqual({
      title: "Update",
      content: "Updated",
      expected_revision: 4,
    });
    expect(String(fetchMock.mock.calls[2]?.[0])).toContain(
      "/m1?expected_revision=5",
    );
  });
  it("parses settings and maps their wire names", async () => {
    const fetchMock = mockResponse({ automatic_extraction: true });
    const client = new ApiClient("https://api.example.test");
    expect(
      await client.updateMemorySettings({ automaticExtraction: true }),
    ).toEqual({ automaticExtraction: true });
    expect(JSON.parse(fetchMock.mock.calls[0]?.[1].body)).toEqual({
      automatic_extraction: true,
    });
  });
});
