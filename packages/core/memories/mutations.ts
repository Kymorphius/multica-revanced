import { useMutation, useQueryClient } from "@tanstack/react-query";
import { api } from "../api";
import type {
  CreateMemoryRequest,
  UpdateMemoryRequest,
  MemorySettings,
} from "../types";
import { memoryKeys } from "./queries";
export function useCreateMemory(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: CreateMemoryRequest) => api.createMemory(data),
    onSettled: () => qc.invalidateQueries({ queryKey: memoryKeys.all(wsId) }),
  });
}
export function useUpdateMemory(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, ...data }: UpdateMemoryRequest & { id: string }) =>
      api.updateMemory(id, data),
    onSettled: () => qc.invalidateQueries({ queryKey: memoryKeys.all(wsId) }),
  });
}
export function useDeleteMemory(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({
      id,
      expectedRevision,
    }: {
      id: string;
      expectedRevision: number;
    }) => api.deleteMemory(id, expectedRevision),
    onSettled: () => qc.invalidateQueries({ queryKey: memoryKeys.all(wsId) }),
  });
}
export function useUpdateMemorySettings(wsId: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (data: MemorySettings) => api.updateMemorySettings(data),
    onSuccess: () =>
      qc.invalidateQueries({ queryKey: memoryKeys.settings(wsId) }),
  });
}
