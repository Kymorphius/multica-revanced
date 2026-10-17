export interface SharedMemory {
  id: string;
  workspaceId: string;
  projectId: string | null;
  title: string;
  content: string;
  source: "manual" | "automatic" | "unknown";
  revision: number;
  createdByType: string;
  createdBy: string;
  createdAt: string;
  updatedAt: string;
}
export interface CreateMemoryRequest {
  title: string;
  content: string;
  projectId?: string;
}
export interface UpdateMemoryRequest {
  title: string;
  content: string;
  expectedRevision: number;
}
export interface MemorySettings {
  automaticExtraction: boolean;
}
export interface MemoryListParams {
  q?: string;
  projectId?: string;
  limit?: number;
  offset?: number;
}
