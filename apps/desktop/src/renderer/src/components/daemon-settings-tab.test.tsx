import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@multica/core/i18n/react";
import { RESOURCES } from "@multica/views/locales";
import type { CodexSettings, DaemonStatus } from "../../../shared/daemon-types";
import { DaemonSettingsTab } from "./daemon-settings-tab";

vi.mock("sonner", () => ({
  toast: { error: vi.fn(), success: vi.fn() },
}));

vi.mock("../platform/daemon-reauth", () => ({
  reauthenticateDaemon: vi.fn(),
}));

const initialSettings: CodexSettings = {
  profile: "desktop-profile",
  savedSharedHome: true,
  effectiveSharedHome: true,
  home: "/Users/test/.codex",
  executable: "/Applications/Codex.app/Contents/Resources/codex",
  editable: true,
  environmentOverride: false,
};

let emitStatus: (status: DaemonStatus) => void = () => {};
const getCodexSettings = vi.fn<() => Promise<CodexSettings>>();
const setCodexSharedHome = vi.fn<(enabled: boolean) => Promise<CodexSettings>>();
const restart = vi.fn();

function installDaemonAPI(status: DaemonStatus = { state: "running" }) {
  Object.defineProperty(window, "daemonAPI", {
    configurable: true,
    value: {
      getPrefs: vi.fn().mockResolvedValue({ autoStart: true, autoStop: false }),
      setPrefs: vi.fn(),
      isCliInstalled: vi.fn().mockResolvedValue(true),
      getStatus: vi.fn().mockResolvedValue(status),
      onStatusChange: vi.fn((handler: (next: DaemonStatus) => void) => {
        emitStatus = handler;
        return vi.fn();
      }),
      getCodexSettings,
      setCodexSharedHome,
      restart,
    },
  });
}

function renderSettings() {
  return render(
    <I18nProvider locale="zh-Hans" resources={RESOURCES}>
      <DaemonSettingsTab />
    </I18nProvider>,
  );
}

function sharedSwitch() {
  return screen.getByRole("switch", { name: "共用 Codex 记忆与会话" });
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason: Error) => void;
  const promise = new Promise<T>((resolvePromise, rejectPromise) => {
    resolve = resolvePromise;
    reject = rejectPromise;
  });
  return { promise, resolve, reject };
}

beforeEach(() => {
  vi.clearAllMocks();
  getCodexSettings.mockReset().mockResolvedValue(initialSettings);
  setCodexSharedHome.mockReset();
  emitStatus = () => {};
  installDaemonAPI();
});

describe("Codex shared-state desktop settings", () => {
  it("reads the persisted setting and exposes storage and native CLI diagnostics", async () => {
    renderSettings();

    await waitFor(() => expect(sharedSwitch()).not.toHaveAttribute("aria-disabled", "true"));
    expect(sharedSwitch()).toBeChecked();
    expect(screen.getByText("当前模式：共用")).toBeInTheDocument();
    expect(screen.getByText(initialSettings.home!)).toHaveClass("select-text", "break-all");
    expect(screen.getByText(initialSettings.executable!)).toHaveClass("select-text", "break-all");
    expect(sharedSwitch()).toHaveAccessibleDescription(
      /重启守护进程后，新会话使用修改后的设置。已有会话继续使用原来的存储。/,
    );
    expect(setCodexSharedHome).not.toHaveBeenCalled();
    expect(restart).not.toHaveBeenCalled();
  });

  it("waits for persistence before updating the switch and refreshes effective mode from daemon status", async () => {
    const save = deferred<CodexSettings>();
    setCodexSharedHome.mockReturnValue(save.promise);
    renderSettings();
    await waitFor(() => expect(sharedSwitch()).not.toHaveAttribute("aria-disabled", "true"));

    fireEvent.click(sharedSwitch());
    expect(setCodexSharedHome).toHaveBeenCalledWith(false);
    expect(sharedSwitch()).toBeChecked();
    expect(sharedSwitch()).toHaveAttribute("aria-disabled", "true");
    expect(screen.getByText("正在保存...")).toBeInTheDocument();

    await act(async () => {
      save.resolve({ ...initialSettings, savedSharedHome: false });
    });
    expect(sharedSwitch()).not.toBeChecked();
    expect(screen.getByText("已保存，重启守护进程后生效")).toBeInTheDocument();
    expect(restart).not.toHaveBeenCalled();

    getCodexSettings.mockResolvedValue({
      ...initialSettings,
      savedSharedHome: false,
      effectiveSharedHome: false,
    });
    act(() => emitStatus({ state: "running", pid: 456 }));
    expect(await screen.findByText("当前模式：独立")).toBeInTheDocument();
  });

  it("preserves the checked value and allows retry when persistence fails", async () => {
    setCodexSharedHome.mockRejectedValue(new Error("write failed"));
    renderSettings();
    await waitFor(() => expect(sharedSwitch()).not.toHaveAttribute("aria-disabled", "true"));

    fireEvent.click(sharedSwitch());

    expect(await screen.findByRole("alert")).toHaveTextContent("无法保存修改，请重试。");
    expect(sharedSwitch()).toBeChecked();
    expect(sharedSwitch()).not.toHaveAttribute("aria-disabled", "true");
    expect(restart).not.toHaveBeenCalled();

    act(() => emitStatus({ state: "running", uptime: "1m" }));
    await waitFor(() => expect(getCodexSettings).toHaveBeenCalledTimes(3));
    expect(screen.getByRole("alert")).toHaveTextContent("无法保存修改，请重试。");
  });

  it("does not let an older read overwrite a completed save", async () => {
    renderSettings();
    await waitFor(() => expect(getCodexSettings).toHaveBeenCalledTimes(2));
    await waitFor(() => expect(sharedSwitch()).not.toHaveAttribute("aria-disabled", "true"));
    const stale = deferred<CodexSettings>();
    getCodexSettings.mockReturnValueOnce(stale.promise);
    act(() => emitStatus({ state: "running", uptime: "1m" }));
    await waitFor(() => expect(getCodexSettings).toHaveBeenCalledTimes(3));
    setCodexSharedHome.mockResolvedValue({ ...initialSettings, savedSharedHome: false });

    fireEvent.click(sharedSwitch());
    await waitFor(() => expect(sharedSwitch()).not.toBeChecked());
    await act(async () => stale.resolve(initialSettings));

    expect(sharedSwitch()).not.toBeChecked();
    expect(screen.getByText("已保存，重启守护进程后生效")).toBeInTheDocument();
  });

  it.each([
    {
      settings: { ...initialSettings, savedSharedHome: false, environmentOverride: true },
      status: { state: "running" } as DaemonStatus,
      reason: "环境变量正在控制此设置，请在守护进程的启动环境中修改。",
    },
    {
      settings: { ...initialSettings, editable: false },
      status: { state: "running", externallyManaged: true } as DaemonStatus,
      reason: "请在运行守护进程的环境中修改此设置。",
    },
    {
      settings: { ...initialSettings, editable: false },
      status: { state: "stopped" } as DaemonStatus,
      reason: "本地守护进程配置档案暂不可用，请检查设置后重新打开此页。",
    },
  ])("explains why sharing cannot be changed: $reason", async ({ settings, status, reason }) => {
    getCodexSettings.mockResolvedValue(settings);
    installDaemonAPI(status);
    renderSettings();

    expect(await screen.findByText(reason)).toBeInTheDocument();
    expect(sharedSwitch()).toHaveAttribute("aria-disabled", "true");
    if (settings.environmentOverride) {
      expect(screen.getByText("当前模式：共用")).toBeInTheDocument();
      expect(screen.queryByText("已保存，重启守护进程后生效")).not.toBeInTheDocument();
    }
    fireEvent.click(sharedSwitch());
    expect(setCodexSharedHome).not.toHaveBeenCalled();
  });

  it("does not imply that a saved setting is currently active when live mode is unavailable", async () => {
    getCodexSettings.mockResolvedValue({ ...initialSettings, effectiveSharedHome: undefined });
    renderSettings();

    expect(await screen.findByText("下次启动守护进程时生效，当前模式暂不可用")).toBeInTheDocument();
    expect(sharedSwitch()).toBeChecked();
    expect(screen.queryByText("当前模式：共用")).not.toBeInTheDocument();
  });

  it("shows read failures and keeps an unresolved setting disabled", async () => {
    getCodexSettings.mockRejectedValue(new Error("read failed"));
    renderSettings();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "无法读取 Codex 设置，请检查守护进程设置后重新打开此页。",
    );
    expect(sharedSwitch()).toHaveAttribute("aria-disabled", "true");
  });
});
