import { infiniteQueryOptions, queryOptions } from "@tanstack/react-query";
import { api } from "../api";
export const memoryKeys = {
  all: (wsId: string) => ["memories", wsId] as const,
  list: (wsId: string, q: string) =>
    [...memoryKeys.all(wsId), "list", q] as const,
  detail: (wsId: string, id: string) =>
    [...memoryKeys.all(wsId), "detail", id] as const,
  settings: (wsId: string) => [...memoryKeys.all(wsId), "settings"] as const,
};
export function memoryListOptions(wsId: string, q: string) {
  return infiniteQueryOptions({
    queryKey: memoryKeys.list(wsId, q),
    initialPageParam: 0,
    queryFn: ({ pageParam }) =>
      api.listMemories({ q, offset: pageParam, limit: 100 }),
    getNextPageParam: (lastPage, pages) =>
      lastPage.length === 100 ? pages.length * 100 : undefined,
  });
}
export function memorySettingsOptions(wsId: string) {
  return queryOptions({
    queryKey: memoryKeys.settings(wsId),
    queryFn: () => api.getMemorySettings(),
  });
}

export function memoryDetailOptions(wsId: string, id: string) {
  return queryOptions({
    queryKey: memoryKeys.detail(wsId, id),
    queryFn: () => api.getMemory(id),
  });
}
