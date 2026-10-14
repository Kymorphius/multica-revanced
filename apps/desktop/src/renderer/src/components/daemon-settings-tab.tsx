import {
  useState,
  useEffect,
  useCallback,
  useId,
  useRef,
  type ReactNode,
} from "react";
import { AlertCircle, Info, LogIn } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { Switch } from "@multica/ui/components/ui/switch";
import { cn } from "@multica/ui/lib/utils";
import { toast } from "sonner";
import {
  SettingsCard,
  SettingsRow,
  SettingsSection,
  SettingsTab,
} from "@multica/views/settings";
import { useT } from "@multica/views/i18n";
import { reauthenticateDaemon } from "../platform/daemon-reauth";
import type {
  CodexSettings,
  DaemonPrefs,
  DaemonStatus,
} from "../../../shared/daemon-types";
import {
  DAEMON_STATE_COLORS,
  formatUptime,
} from "../../../shared/daemon-types";
import { daemonStateLabel } from "./daemon-i18n";

// One row inside the diagnostics block. Values that are likely to be
// long IDs / URLs render as monospaced + truncated with a tooltip.
function DiagnosticsRow({
  label,
  value,
  mono,
  wrap,
}: {
  label: string;
  value: ReactNode;
  mono?: boolean;
  wrap?: boolean;
}) {
  return (
    <div className="grid grid-cols-[140px_minmax(0,1fr)] items-baseline gap-3 py-1.5">
      <span className="text-caption text-muted-foreground">{label}</span>
      <span
        className={cn(
          "min-w-0 text-body",
          wrap ? "select-text break-all whitespace-normal" : "truncate",
          mono && "font-mono text-caption",
        )}
        title={typeof value === "string" ? value : undefined}
      >
        {value}
      </span>
    </div>
  );
}

export function DaemonSettingsTab() {
  const { t } = useT("settings");
  const [prefs, setPrefs] = useState<DaemonPrefs>({ autoStart: true, autoStop: false });
  const [cliInstalled, setCliInstalled] = useState<boolean | null>(null);
  const [saving, setSaving] = useState(false);
  const [status, setStatus] = useState<DaemonStatus>({ state: "stopped" });
  const [reauthLoading, setReauthLoading] = useState(false);
  const [codexSettings, setCodexSettings] = useState<CodexSettings | null>(null);
  const [codexLoading, setCodexLoading] = useState(true);
  const [codexSaving, setCodexSaving] = useState(false);
  const [codexError, setCodexError] = useState<{
    kind: "read" | "save";
    message: string;
  } | null>(null);
  const codexLabelId = useId();
  const codexDescriptionId = useId();
  const mounted = useRef(false);
  const codexRequest = useRef(0);
  const codexSavePending = useRef(false);
  const codexRefreshPending = useRef(false);

  const refreshCodexSettings = useCallback(async () => {
    if (codexSavePending.current) {
      codexRefreshPending.current = true;
      return;
    }
    const request = ++codexRequest.current;
    try {
      const settings = await window.daemonAPI.getCodexSettings();
      if (mounted.current && request === codexRequest.current) {
        setCodexSettings(settings);
        setCodexError((error) => (error?.kind === "read" ? null : error));
      }
    } catch {
      if (mounted.current && request === codexRequest.current) {
        setCodexError({
          kind: "read",
          message: t(($) => $.desktop.daemon.codex_load_failed),
        });
      }
    } finally {
      if (mounted.current && request === codexRequest.current) {
        setCodexLoading(false);
      }
    }
  }, [t]);

  useEffect(() => {
    mounted.current = true;
    return () => {
      mounted.current = false;
    };
  }, []);

  useEffect(() => {
    window.daemonAPI.getPrefs().then(setPrefs);
    window.daemonAPI.isCliInstalled().then(setCliInstalled);
    window.daemonAPI.getStatus().then(setStatus);
    return window.daemonAPI.onStatusChange(setStatus);
  }, []);

  useEffect(() => {
    void refreshCodexSettings();
  }, [status, refreshCodexSettings]);

  const updateCodexSharedHome = useCallback(async (sharedHome: boolean) => {
    if (codexSavePending.current) return;
    codexSavePending.current = true;
    const request = ++codexRequest.current;
    setCodexSaving(true);
    setCodexError(null);
    try {
      const updated = await window.daemonAPI.setCodexSharedHome(sharedHome);
      if (mounted.current && request === codexRequest.current) {
        setCodexSettings(updated);
      }
    } catch {
      if (mounted.current && request === codexRequest.current) {
        setCodexError({
          kind: "save",
          message: t(($) => $.desktop.daemon.codex_save_failed),
        });
      }
    } finally {
      codexSavePending.current = false;
      if (mounted.current && request === codexRequest.current) {
        setCodexSaving(false);
        if (codexRefreshPending.current) {
          codexRefreshPending.current = false;
          void refreshCodexSettings();
        }
      }
    }
  }, [refreshCodexSettings, t]);

  const handleReauth = useCallback(async () => {
    setReauthLoading(true);
    await reauthenticateDaemon(t);
    setReauthLoading(false);
  }, [t]);

  const updatePref = useCallback(
    async (key: keyof DaemonPrefs, value: boolean) => {
      setSaving(true);
      try {
        const updated = await window.daemonAPI.setPrefs({ [key]: value });
        setPrefs(updated);
        toast.success(t(($) => $.desktop.daemon.settings_saved), {
          id: "settings-auto-save",
        });
      } catch (error) {
        toast.error(
          error instanceof Error
            ? error.message
            : t(($) => $.desktop.daemon.settings_save_failed),
        );
      } finally {
        setSaving(false);
      }
    },
    [t],
  );

  // The daemon runs somewhere the app can't drive (e.g. inside WSL2 behind a
  // Windows desktop): /health is reachable but the lifecycle CLI can't reach
  // its process. Auto-start/auto-stop can't work, so disable them and say why
  // rather than letting the toggles silently no-op. See #3916.
  const externallyManaged = status.externallyManaged === true;
  const codexUnavailable = externallyManaged
    ? t(($) => $.desktop.daemon.codex_external_unavailable)
    : codexSettings?.environmentOverride
      ? t(($) => $.desktop.daemon.codex_environment_override)
      : codexSettings && !codexSettings.editable
        ? t(($) => $.desktop.daemon.codex_profile_unavailable)
        : null;
  const codexState = codexError?.kind === "read"
    ? null
    : codexLoading
    ? t(($) => $.desktop.daemon.codex_loading)
    : codexSaving
      ? t(($) => $.desktop.daemon.codex_saving)
      : codexSettings
        ? typeof codexSettings.effectiveSharedHome !== "boolean"
          ? t(($) => $.desktop.daemon.codex_next_start)
          : !codexSettings.environmentOverride && codexSettings.savedSharedHome !== codexSettings.effectiveSharedHome
            ? t(($) => $.desktop.daemon.codex_saved_pending)
            : codexSettings.effectiveSharedHome
              ? t(($) => $.desktop.daemon.codex_active_shared)
              : t(($) => $.desktop.daemon.codex_active_isolated)
        : null;

  return (
    <SettingsTab
      title={t(($) => $.desktop.daemon.title)}
    >

      {status.state === "auth_expired" && (
        <div className="mt-4 flex items-start gap-3 rounded-lg border border-destructive/40 bg-destructive/5 px-4 py-3">
          <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" />
          <div className="min-w-0 flex-1">
            <p className="text-body font-medium text-destructive">
              {t(($) => $.desktop.daemon.signin_expired)}
            </p>
            <p className="mt-0.5 text-body text-muted-foreground">
              {t(($) => $.desktop.daemon.signin_expired_description)}
            </p>
          </div>
          <Button
            size="sm"
            className="shrink-0"
            onClick={handleReauth}
            disabled={reauthLoading}
          >
            <LogIn className="size-3.5 mr-1.5" />
            {t(($) => $.desktop.daemon.signin_again)}
          </Button>
        </div>
      )}

      {externallyManaged && (
        <div className="mt-4 flex items-start gap-3 rounded-lg border bg-muted/30 px-4 py-3">
          <Info className="mt-0.5 size-4 shrink-0 text-muted-foreground" />
          <p className="min-w-0 text-body text-muted-foreground">
            {t(($) => $.desktop.daemon.external_description_before)}{" "}
            <code className="font-mono text-caption">multica daemon start</code> /{" "}
            <code className="font-mono text-caption">multica daemon stop</code>
            {t(($) => $.desktop.daemon.external_description_after)}
          </p>
        </div>
      )}

      <SettingsCard>
        <SettingsRow
          label={t(($) => $.desktop.daemon.auto_start_title)}
          description={t(($) => $.desktop.daemon.auto_start_description)}
        >
          <Switch
            checked={prefs.autoStart}
            onCheckedChange={(checked) => updatePref("autoStart", checked)}
            disabled={saving || externallyManaged}
          />
        </SettingsRow>

        <SettingsRow
          label={t(($) => $.desktop.daemon.auto_stop_title)}
          description={t(($) => $.desktop.daemon.auto_stop_description)}
        >
          <Switch
            checked={prefs.autoStop}
            onCheckedChange={(checked) => updatePref("autoStop", checked)}
            disabled={saving || externallyManaged}
          />
        </SettingsRow>

        <SettingsRow
          label={t(($) => $.desktop.daemon.cli_status)}
          description={
            cliInstalled === null
              ? t(($) => $.desktop.daemon.cli_checking)
              : cliInstalled
                ? t(($) => $.desktop.daemon.cli_installed)
                : t(($) => $.desktop.daemon.cli_missing)
          }
        >
          {cliInstalled === false && (
            <Button
              variant="outline"
              size="sm"
              onClick={() =>
                window.desktopAPI.openExternal(
                  "https://github.com/multica-ai/multica#cli-installation",
                )
              }
            >
              {t(($) => $.desktop.daemon.installation_guide)}
            </Button>
          )}
          {cliInstalled !== false && <span />}
        </SettingsRow>
      </SettingsCard>

      <SettingsCard>
        <SettingsRow
          label={
            <span id={codexLabelId}>
              {t(($) => $.desktop.daemon.codex_shared_title)}
            </span>
          }
          description={
            <div id={codexDescriptionId}>
              <p>{t(($) => $.desktop.daemon.codex_shared_description)}</p>
              {codexState && (
                <p className="mt-1" role="status">{codexState}</p>
              )}
              {codexUnavailable && <p className="mt-1">{codexUnavailable}</p>}
              {codexError && (
                <p className="mt-1 text-destructive" role="alert">
                  {codexError.message}
                </p>
              )}
            </div>
          }
        >
          <Switch
            aria-labelledby={codexLabelId}
            aria-describedby={codexDescriptionId}
            aria-busy={codexLoading || codexSaving}
            checked={codexSettings?.savedSharedHome ?? false}
            onCheckedChange={updateCodexSharedHome}
            disabled={
              codexLoading || codexSaving || codexError?.kind === "read" ||
              !!codexUnavailable || !codexSettings
            }
          />
        </SettingsRow>
      </SettingsCard>

      {/* Diagnostics — moved out of the logs panel so the panel can focus
          on logs. These fields matter for support tickets and bug reports,
          not for everyday use. */}
      <SettingsSection
        title={t(($) => $.desktop.daemon.diagnostics_title)}
      >
        <SettingsCard>
          <div className="px-4 py-2">
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.state)}
            value={
              <span className="inline-flex items-center gap-1.5">
                <span
                  className={cn(
                    "size-1.5 rounded-full",
                    DAEMON_STATE_COLORS[status.state],
                  )}
                />
                {daemonStateLabel(status.state, t)}
              </span>
            }
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.uptime)}
            value={status.uptime ? formatUptime(status.uptime) : "—"}
          />
          <DiagnosticsRow
            label="PID"
            value={status.pid ?? "—"}
            mono={!!status.pid}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.daemon_id)}
            value={status.daemonId ?? "—"}
            mono={!!status.daemonId}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.profile)}
            value={codexSettings?.profile || status.profile || "default"}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.server_url)}
            value={status.serverUrl ?? "—"}
            mono={!!status.serverUrl}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.device_name)}
            value={status.deviceName ?? "—"}
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.workspaces)}
            value={
              typeof status.workspaceCount === "number"
                ? status.workspaceCount
                : "—"
            }
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.codex_shared_home)}
            value={codexSettings?.home ?? t(($) => $.desktop.daemon.codex_unavailable)}
            mono
            wrap
          />
          <DiagnosticsRow
            label={t(($) => $.desktop.daemon.codex_executable)}
            value={codexSettings?.executable ?? t(($) => $.desktop.daemon.codex_unavailable)}
            mono
            wrap
          />
          </div>
        </SettingsCard>
      </SettingsSection>
    </SettingsTab>
  );
}
