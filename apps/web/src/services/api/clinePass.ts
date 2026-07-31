import { apiClient } from './client';

export type ThirdPartyQuotaProvider = 'clinepass' | 'opencode_go';

export type ThirdPartyQuotaAccount = {
  id: string;
  provider: ThirdPartyQuotaProvider;
  label: string;
  workspaceId?: string;
  serverFunctionId?: string;
  createdAtMs: number;
  updatedAtMs: number;
};

export type ThirdPartyQuotaUsageLimit = {
  type: string;
  percentUsed: number;
  resetsAt?: string;
};

export type ThirdPartyQuotaUsageLimits = {
  accountId: string;
  provider: ThirdPartyQuotaProvider;
  fetchedAtMs: number;
  limits: ThirdPartyQuotaUsageLimit[];
  mine?: boolean;
  useBalance?: boolean;
  regions?: string[];
};

export type CreateThirdPartyQuotaAccount = {
  provider: ThirdPartyQuotaProvider;
  label: string;
  credential: string;
  workspaceId?: string;
  serverFunctionId?: string;
};

export type UpdateThirdPartyQuotaAccount = {
  label?: string;
  credential?: string;
  workspaceId?: string;
  serverFunctionId?: string;
};

const basePath = '/third-party-quota/accounts';

export const thirdPartyQuotaApi = {
  list: () => apiClient.get<{ items: ThirdPartyQuotaAccount[] }>(basePath),
  create: (request: CreateThirdPartyQuotaAccount) =>
    apiClient.post<ThirdPartyQuotaAccount>(basePath, request),
  update: (id: string, request: UpdateThirdPartyQuotaAccount) =>
    apiClient.patch<ThirdPartyQuotaAccount>(`${basePath}/${encodeURIComponent(id)}`, request),
  delete: (id: string) =>
    apiClient.delete<{ ok: boolean }>(`${basePath}/${encodeURIComponent(id)}`),
  getUsageLimits: (id: string) =>
    apiClient.get<ThirdPartyQuotaUsageLimits>(
      `${basePath}/${encodeURIComponent(id)}/usage-limits`
    ),
};
