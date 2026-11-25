import { apiFetch } from './http';
export interface ExtensionSelection {
  id: string;
  version: number;
  catalog_version?: number;
  scopes?: string[];
  permissions?: string[];
  session_memory?: boolean;
  session_scope?: string;
}
export interface WorkspaceExtensionsInput {
  connectors?: ExtensionSelection[];
  memory?: ExtensionSelection[];
  knowledge?: ExtensionSelection[];
  plugins?: { id: string; version: string }[];
}
export interface WorkspacePlugin {
  id: string;
  name: string;
  package: string;
  version: string;
  available: boolean;
  reason?: string;
}
export interface WorkspaceExtension {
  kind: 'connector' | 'memory' | 'knowledge' | 'plugin';
  backend?: 'openviking' | 'mem0' | 'graphiti' | 'qdrant' | 'weaviate' | 'milvus';
  id: string;
  name: string;
  version?: number;
  package_version?: string;
  scopes?: string[];
  permissions?: string[];
  session_memory?: boolean;
  session_scope?: string;
  tools?: string[];
  status: string;
  reason?: string;
}
export interface WorkspaceSkill {
  id: string;
  name: string;
  version: number;
}
export async function readWorkspaceExtensions<T>(
  id: string,
  signal?: AbortSignal,
  skills = false,
  fetcher?: typeof fetch,
): Promise<T> {
  const response = await apiFetch(
    '/v1/workspace-extensions/' + encodeURIComponent(id) + (skills ? '/skills' : ''),
    { timeoutMs: 10_000, signal, fetcher },
  );
  return response.json();
}
