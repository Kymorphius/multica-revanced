import { afterEach, describe, expect, it, vi } from "vitest";
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { api, ApiError } from "@multica/core/api";
import type { SharedMemory } from "@multica/core/types";
import { renderWithI18n } from "../test/i18n";
import { MemoryEditorDialog } from "./memories-page";

vi.mock("@multica/core/api", async (original) => ({
  ...(await original<typeof import("@multica/core/api")>()),
  api: { createMemory: vi.fn(), updateMemory: vi.fn(), getMemory: vi.fn() },
}));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
const memory: SharedMemory = {
  id: "m1",
  workspaceId: "w1",
  projectId: null,
  title: "Build",
  content: "Old instructions",
  source: "automatic",
  revision: 2,
  createdBy: "a1",
  createdByType: "agent",
  createdAt: "",
  updatedAt: "",
};
function renderEditor(item: SharedMemory | null = null) {
  const onClose = vi.fn();
  const client = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  renderWithI18n(
    <QueryClientProvider client={client}>
      <MemoryEditorDialog
        wsId="w1"
        memory={item}
        projects={[]}
        onClose={onClose}
      />
    </QueryClientProvider>,
  );
  return { onClose };
}

describe("memory editor", () => {
  it("waits for create success before closing and keeps draft visible on failure", async () => {
    const user = userEvent.setup();
    vi.mocked(api.createMemory).mockRejectedValueOnce(new Error("offline"));
    const { onClose } = renderEditor();
    await user.type(screen.getByLabelText("Title"), "Preference");
    await user.type(screen.getByLabelText("Content"), "Use focused checks");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Your draft is preserved",
    );
    expect(screen.getByLabelText("Content")).toHaveValue("Use focused checks");
    expect(onClose).not.toHaveBeenCalled();
    vi.mocked(api.createMemory).mockResolvedValueOnce(memory);
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onClose).toHaveBeenCalledOnce());
  });
  it("preserves a conflicting draft, shows latest saved content, and requires explicit replacement", async () => {
    const user = userEvent.setup();
    vi.mocked(api.updateMemory).mockRejectedValueOnce(
      new ApiError("conflict", 409, "Conflict"),
    );
    vi.mocked(api.getMemory).mockResolvedValueOnce({
      ...memory,
      content: "Another member's correction",
      revision: 3,
    });
    const { onClose } = renderEditor(memory);
    await user.clear(screen.getByLabelText("Content"));
    await user.type(screen.getByLabelText("Content"), "My draft");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(
      await screen.findByText("Another member's correction"),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Content")).toHaveValue("My draft");
    expect(screen.getByRole("button", { name: "Save" })).toBeDisabled();
    expect(onClose).not.toHaveBeenCalled();
    expect(api.updateMemory).toHaveBeenCalledWith("m1", {
      title: "Build",
      content: "My draft",
      expectedRevision: 2,
    });
    await user.click(
      screen.getByRole("button", { name: "Replace draft with latest version" }),
    );
    expect(screen.getByLabelText("Content")).toHaveValue(
      "Another member's correction",
    );
    vi.mocked(api.updateMemory).mockResolvedValueOnce({
      ...memory,
      revision: 4,
    });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() =>
      expect(api.updateMemory).toHaveBeenLastCalledWith(
        "m1",
        expect.objectContaining({ expectedRevision: 3 }),
      ),
    );
  });
});
