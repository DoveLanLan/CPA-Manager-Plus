import { apiClient } from './client';

export type ClinePassAccount = {
  id: string;
  label: string;
  createdAtMs: number;
  updatedAtMs: number;
};

export type ClinePassUsageLimit = {
  type: string;
  percentUsed: number;
  resetsAt?: string;
};

export type ClinePassUsageLimits = {
  accountId: string;
  fetchedAtMs: number;
  limits: ClinePassUsageLimit[];
};

export type CreateClinePassAccount = {
  label: string;
  sessionId: string;
};

export type UpdateClinePassAccount = {
  label?: string;
  sessionId?: string;
};

const basePath = '/clinepass/accounts';

export const clinePassApi = {
  list: () => apiClient.get<{ items: ClinePassAccount[] }>(basePath),
  create: (request: CreateClinePassAccount) =>
    apiClient.post<ClinePassAccount>(basePath, request),
  update: (id: string, request: UpdateClinePassAccount) =>
    apiClient.patch<ClinePassAccount>(`${basePath}/${encodeURIComponent(id)}`, request),
  delete: (id: string) =>
    apiClient.delete<{ ok: boolean }>(`${basePath}/${encodeURIComponent(id)}`),
  getUsageLimits: (id: string) =>
    apiClient.get<ClinePassUsageLimits>(
      `${basePath}/${encodeURIComponent(id)}/usage-limits`
    ),
};
