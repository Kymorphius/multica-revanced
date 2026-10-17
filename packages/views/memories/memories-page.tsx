"use client";

import { useState } from "react";
import {
  useInfiniteQuery,
  useQuery,
  useQueryClient,
} from "@tanstack/react-query";
import { BookOpenText, Pencil, Plus, Search, Trash2 } from "lucide-react";
import { ApiError } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useCurrentMember } from "@multica/core/permissions";
import { projectListOptions } from "@multica/core/projects/queries";
import {
  memoryDetailOptions,
  memoryListOptions,
  memorySettingsOptions,
  useCreateMemory,
  useUpdateMemory,
  useDeleteMemory,
  useUpdateMemorySettings,
} from "@multica/core/memories";
import type { SharedMemory } from "@multica/core/types";
import { Button } from "@multica/ui/components/ui/button";
import { Input } from "@multica/ui/components/ui/input";
import { Label } from "@multica/ui/components/ui/label";
import { Textarea } from "@multica/ui/components/ui/textarea";
import { Switch } from "@multica/ui/components/ui/switch";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@multica/ui/components/ui/dialog";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import {
  CollectionPageHeader,
  CollectionPageState,
} from "../layout/collection-page";
import { useLocale, useT } from "../i18n";

export function MemoriesPage() {
  const { t } = useT("memories");
  const locale = useLocale();
  const wsId = useWorkspaceId();
  const { role } = useCurrentMember(wsId);
  const canWrite = role === "owner" || role === "admin" || role === "member";
  const canConfigure = role === "owner" || role === "admin";
  const [query, setQuery] = useState("");
  const [editor, setEditor] = useState<SharedMemory | "new" | null>(null);
  const [deleting, setDeleting] = useState<SharedMemory | null>(null);
  const memories = useInfiniteQuery(memoryListOptions(wsId, query.trim()));
  const projects = useQuery(projectListOptions(wsId));
  const settings = useQuery(memorySettingsOptions(wsId));
  const updateSettings = useUpdateMemorySettings(wsId);
  const items = memories.data?.pages.flat() ?? [];
  return (
    <div className="flex h-full flex-col">
      <CollectionPageHeader
        icon={BookOpenText}
        title={t(($) => $.title)}
        actions={
          canWrite && (
            <Button size="sm" onClick={() => setEditor("new")}>
              <Plus />
              {t(($) => $.create)}
            </Button>
          )
        }
      />
      <div className="min-h-0 flex-1 overflow-auto p-6">
        <div className="mx-auto max-w-4xl space-y-5">
          <p className="text-body text-muted-foreground">
            {t(($) => $.sharing)}
          </p>
          <div className="rounded-lg border border-surface-border p-4">
            <div className="flex items-center justify-between gap-4">
              <Label htmlFor="automatic-memory">
                {t(($) => $.automatic.title)}
              </Label>
              <Switch
                id="automatic-memory"
                checked={settings.data?.automaticExtraction ?? false}
                disabled={
                  !canConfigure || !settings.data || updateSettings.isPending
                }
                aria-busy={updateSettings.isPending}
                onCheckedChange={(checked) =>
                  updateSettings.mutate({ automaticExtraction: checked })
                }
              />
            </div>
            <p className="mt-2 text-caption text-muted-foreground">
              {t(($) => $.automatic.description)}
            </p>
            {!canConfigure && (
              <p className="mt-2 text-caption text-muted-foreground">
                {t(($) => $.automatic.permission)}
              </p>
            )}
            {(settings.isError || updateSettings.isError) && (
              <p role="alert" className="mt-2 text-caption text-destructive">
                {t(($) => $.automatic.failed)}
              </p>
            )}
          </div>
          <div className="relative">
            <Search className="pointer-events-none absolute left-3 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              aria-label={t(($) => $.search)}
              placeholder={t(($) => $.search)}
              className="pl-9"
              value={query}
              onChange={(e) => setQuery(e.target.value)}
            />
          </div>
          {memories.isLoading ? (
            <CollectionPageState
              icon={BookOpenText}
              title={t(($) => $.loading)}
            />
          ) : memories.isError ? (
            <CollectionPageState
              icon={BookOpenText}
              tone="destructive"
              role="alert"
              title={t(($) => $.load_failed)}
              actions={
                <Button variant="outline" onClick={() => memories.refetch()}>
                  {t(($) => $.retry)}
                </Button>
              }
            />
          ) : items.length === 0 ? (
            <CollectionPageState
              icon={BookOpenText}
              title={query ? t(($) => $.no_results) : t(($) => $.empty)}
            />
          ) : (
            <div className="space-y-3">
              {items.map((memory) => (
                <article
                  key={memory.id}
                  className="rounded-lg border border-surface-border p-4"
                >
                  <div className="flex items-start justify-between gap-3">
                    <h2 className="min-w-0 break-words text-body font-medium">
                      {memory.title}
                    </h2>
                    {canWrite && (
                      <div className="flex shrink-0 gap-1">
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label={t(($) => $.edit_named, {
                            title: memory.title,
                          })}
                          onClick={() => setEditor(memory)}
                        >
                          <Pencil />
                        </Button>
                        <Button
                          size="icon-sm"
                          variant="ghost"
                          aria-label={t(($) => $.delete_named, {
                            title: memory.title,
                          })}
                          onClick={() => setDeleting(memory)}
                        >
                          <Trash2 />
                        </Button>
                      </div>
                    )}
                  </div>
                  <p className="mt-1 break-words text-caption text-muted-foreground">
                    {memory.projectId
                      ? t(($) => $.project_scope, {
                          name:
                            projects.data?.find(
                              (p) => p.id === memory.projectId,
                            )?.title ?? memory.projectId,
                        })
                      : t(($) => $.workspace_scope)}{" "}
                    · {t(($) => $.source[memory.source])}
                    {memory.updatedAt &&
                      ` · ${new Date(memory.updatedAt).toLocaleDateString(locale)}`}
                  </p>
                  <p className="mt-3 whitespace-pre-wrap break-words text-body">
                    {memory.content}
                  </p>
                </article>
              ))}
              {memories.hasNextPage && (
                <Button
                  variant="outline"
                  disabled={memories.isFetchingNextPage}
                  aria-busy={memories.isFetchingNextPage}
                  onClick={() => memories.fetchNextPage()}
                >
                  {t(($) => $.load_more)}
                </Button>
              )}
            </div>
          )}
        </div>
      </div>
      {editor !== null && (
        <MemoryEditorDialog
          wsId={wsId}
          memory={editor === "new" ? null : editor}
          projects={(projects.data ?? []).map((project) => ({
            id: project.id,
            name: project.title,
          }))}
          onClose={() => setEditor(null)}
        />
      )}
      {deleting && (
        <DeleteMemoryDialog
          wsId={wsId}
          memory={deleting}
          onClose={() => setDeleting(null)}
        />
      )}
    </div>
  );
}

export function MemoryEditorDialog({
  wsId,
  memory,
  projects,
  onClose,
}: {
  wsId: string;
  memory: SharedMemory | null;
  projects: { id: string; name: string }[];
  onClose: () => void;
}) {
  const { t } = useT("memories");
  const queryClient = useQueryClient();
  const create = useCreateMemory(wsId);
  const update = useUpdateMemory(wsId);
  const [title, setTitle] = useState(memory?.title ?? "");
  const [content, setContent] = useState(memory?.content ?? "");
  const [projectId, setProjectId] = useState(memory?.projectId ?? "");
  const [baseline, setBaseline] = useState(memory);
  const [latest, setLatest] = useState<SharedMemory | null>(null);
  const [error, setError] = useState("");
  const [checking, setChecking] = useState(false);
  const pending = create.isPending || update.isPending || checking;
  const submit = async () => {
    setError("");
    try {
      if (baseline)
        await update.mutateAsync({
          id: baseline.id,
          title: title.trim(),
          content: content.trim(),
          expectedRevision: baseline.revision,
        });
      else
        await create.mutateAsync({
          title: title.trim(),
          content: content.trim(),
          projectId: projectId || undefined,
        });
      onClose();
    } catch (err) {
      if (err instanceof ApiError && err.status === 409 && baseline) {
        setError(t(($) => $.conflict));
        setChecking(true);
        try {
          const current = await queryClient.fetchQuery(
            memoryDetailOptions(wsId, baseline.id),
          );
          if (current.id !== baseline.id)
            throw new Error("Invalid memory response");
          setLatest(current);
        } catch {
          setError(t(($) => $.conflict_load_failed));
        } finally {
          setChecking(false);
        }
      } else setError(t(($) => $.save_failed));
    }
  };
  const valid =
    title.trim() &&
    content.trim() &&
    [...title].length <= 160 &&
    [...content].length <= 4000;
  return (
    <Dialog open onOpenChange={(open) => !open && !pending && onClose()}>
      <DialogContent
        className="max-h-[90vh] overflow-y-auto sm:max-w-2xl"
        showCloseButton={!pending}
      >
        <DialogHeader>
          <DialogTitle>
            {memory ? t(($) => $.edit) : t(($) => $.create)}
          </DialogTitle>
          <DialogDescription>{t(($) => $.editor_hint)}</DialogDescription>
        </DialogHeader>
        <div className="space-y-4">
          <div className="space-y-2">
            <Label htmlFor="memory-title">{t(($) => $.fields.title)}</Label>
            <Input
              id="memory-title"
              autoFocus
              value={title}
              disabled={pending}
              onChange={(e) => setTitle(e.target.value)}
            />
            <p className="text-caption text-muted-foreground">
              {[...title].length}/160
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="memory-content">{t(($) => $.fields.content)}</Label>
            <Textarea
              id="memory-content"
              rows={8}
              value={content}
              disabled={pending}
              onChange={(e) => setContent(e.target.value)}
            />
            <p className="text-caption text-muted-foreground">
              {[...content].length}/4000
            </p>
          </div>
          <div className="space-y-2">
            <Label htmlFor="memory-project">{t(($) => $.fields.scope)}</Label>
            <select
              id="memory-project"
              className="w-full rounded-md border border-input bg-background px-3 py-2 text-body"
              value={projectId}
              disabled={Boolean(memory) || pending}
              onChange={(e) => setProjectId(e.target.value)}
            >
              <option value="">{t(($) => $.workspace_scope)}</option>
              {projectId &&
                !projects.some((project) => project.id === projectId) && (
                  <option value={projectId}>{projectId}</option>
                )}
              {projects.map((p) => (
                <option key={p.id} value={p.id}>
                  {p.name}
                </option>
              ))}
            </select>
          </div>
          {error && (
            <p role="alert" className="text-body text-destructive">
              {error}
            </p>
          )}
          {latest && (
            <div className="space-y-3 rounded-lg border border-surface-border p-4">
              <h3 className="text-body font-medium">{t(($) => $.latest)}</h3>
              <p className="break-words text-body font-medium">
                {latest.title}
              </p>
              <p className="whitespace-pre-wrap break-words text-body">
                {latest.content}
              </p>
              <Button
                variant="outline"
                onClick={() => {
                  setTitle(latest.title);
                  setContent(latest.content);
                  setBaseline(latest);
                  setLatest(null);
                  setError("");
                }}
              >
                {t(($) => $.reload_latest)}
              </Button>
            </div>
          )}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={pending} onClick={onClose}>
            {t(($) => $.cancel)}
          </Button>
          <Button
            disabled={!valid || pending || Boolean(latest)}
            aria-busy={pending}
            onClick={submit}
          >
            {pending ? t(($) => $.saving) : t(($) => $.save)}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function DeleteMemoryDialog({
  wsId,
  memory,
  onClose,
}: {
  wsId: string;
  memory: SharedMemory;
  onClose: () => void;
}) {
  const { t } = useT("memories");
  const remove = useDeleteMemory(wsId);
  const [error, setError] = useState("");
  return (
    <AlertDialog
      open
      onOpenChange={(open) => !open && !remove.isPending && onClose()}
    >
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{t(($) => $.delete_title)}</AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.delete_description, { title: memory.title })}
          </AlertDialogDescription>
        </AlertDialogHeader>
        {error && (
          <p role="alert" className="text-body text-destructive">
            {error}
          </p>
        )}
        <AlertDialogFooter>
          <Button
            variant="outline"
            disabled={remove.isPending}
            onClick={onClose}
          >
            {t(($) => $.cancel)}
          </Button>
          <Button
            variant="destructive"
            disabled={remove.isPending || Boolean(error)}
            aria-busy={remove.isPending}
            onClick={() =>
              remove.mutate(
                { id: memory.id, expectedRevision: memory.revision },
                {
                  onSuccess: onClose,
                  onError: (err) =>
                    setError(
                      err instanceof ApiError && err.status === 409
                        ? t(($) => $.delete_conflict)
                        : t(($) => $.delete_failed),
                    ),
                },
              )
            }
          >
            {t(($) => $.delete)}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
