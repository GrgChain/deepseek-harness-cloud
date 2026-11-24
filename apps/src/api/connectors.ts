import { apiFetch } from './http';
import type { CurrentUser } from './identity';
export interface MCPTool {
  name: string;
  description: string;
  input_schema: Record<string, unknown>;
  definition: { annotations?: { readOnlyHint?: boolean; destructiveHint?: boolean } };
  hash: string;
  enabled: boolean;
  change?: string;
}
export interface Connector {
  id: string;
  name: string;
  description: string;
  endpoint_id: string;
  url?: string;
  catalog_id?: string;
  protocol_version: string;
  auth_type: string;
  version: number;
  catalog_version: number;
  enabled: boolean;
  status: string;
  server_info: {
    name?: string;
    version?: string;
    removed_tools?: string[];
    verification_error?: string;
    capabilities?: Record<string, unknown>;
  };
  tools: MCPTool[];
  verified_at: string | null;
  updated_at: string;
}
export interface MCPEndpoint {
  id: string;
  name: string;
  url: string;
  catalog_id?: string;
}
export interface ConnectorPreset {
  id: string;
  name: string;
  description: string;
  category: string;
  mode: string;
  auth_kind: string;
  default_url?: string;
  doc_url: string;
  guide_url: string;
  setup_hint: string;
  requirements: string[];
  color: string;
}
export interface ConnectorCatalog {
  version: number;
  source_url: string;
  entries: ConnectorPreset[];
}
export interface ConnectorList {
  items: Connector[];
  endpoints: MCPEndpoint[];
  catalog: ConnectorCatalog;
}
export interface MCPCall {
  id: string;
  connector_id: string;
  tool: string;
  status: string;
  reason: string;
  created_at: string;
  completed_at: string | null;
  replayed?: boolean;
  result?: unknown;
}
const connectorPassthroughCodes = new Set([
  'CONNECTOR_INVALID',
  'CONNECTOR_UNAVAILABLE',
  'CONNECTOR_BUSY',
  'CONNECTOR_CONFLICT',
  'ENDPOINT_NOT_APPROVED',
  'VERSION_CONFLICT',
  'IDEMPOTENCY_CONFLICT',
  'PAYLOAD_TOO_LARGE',
  'NOT_FOUND',
]);

export class ConnectorsClient {
  constructor(private readonly fetcher: typeof fetch = (...args) => fetch(...args)) {}
  async request<T>(
    user: CurrentUser,
    path = '',
    body?: unknown,
    method = body === undefined ? 'GET' : 'POST',
    signal?: AbortSignal,
  ): Promise<T> {
    const response = await apiFetch('/v1/connectors' + path, {
      method,
      user,
      body,
      timeoutMs: 127_000,
      signal,
      fetcher: this.fetcher,
      passthroughError: (code) => connectorPassthroughCodes.has(code),
    });
    return response.json();
  }
}
