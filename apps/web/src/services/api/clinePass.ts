import { apiClient } from './client';

export type ThirdPartyQuotaProvider = 'clinepass' | 'opencode_go';

/**
 * How an account authenticates upstream. `api_key` is the durable choice;
 * `session` is a browser cookie that expires on the vendor's own schedule.
 */
export type ThirdPartyQuotaCredentialKind =
  | 'api_key'
  | 'access_token'
  | 'refresh_token'
  | 'session';

/** What the provider recorded, plus whether each total could be read. */
export type ThirdPartyQuotaTokenUsage = {
  monthToDate: number;
  lifetime: number;
  lifetimePartial?: boolean;
  lifetimeSyncedAtMs?: number;
  lifetimeCached?: boolean;
  monthToDateFailed?: boolean;
  lifetimeFailed?: boolean;
};

export type ThirdPartyQuotaAccount = {
  id: string;
  provider: ThirdPartyQuotaProvider;
  label: string;
  credentialKind?: ThirdPartyQuotaCredentialKind;
  accountEmail?: string;
  accessTokenExpiresAtMs?: number;
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
  credentialKind?: ThirdPartyQuotaCredentialKind;
  fetchedAtMs: number;
  limits: ThirdPartyQuotaUsageLimit[];
  mine?: boolean;
  useBalance?: boolean;
  regions?: string[];
  tokens?: ThirdPartyQuotaTokenUsage;
};

export type CreateThirdPartyQuotaAccount = {
  provider: ThirdPartyQuotaProvider;
  label: string;
  credential: string;
  credentialKind?: ThirdPartyQuotaCredentialKind;
  workspaceId?: string;
  serverFunctionId?: string;
};

export type UpdateThirdPartyQuotaAccount = {
  label?: string;
  credential?: string;
  credentialKind?: ThirdPartyQuotaCredentialKind;
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
  /**
   * Reads the quota windows, the month-to-date token total, and the recorded
   * all-time token total. `forceTokens` re-walks the all-time total instead of
   * reusing the recorded one, which costs one upstream request per month.
   */
  getUsageLimits: (id: string, options?: { forceTokens?: boolean }) =>
    apiClient.get<ThirdPartyQuotaUsageLimits>(
      `${basePath}/${encodeURIComponent(id)}/usage-limits${
        options?.forceTokens ? '?forceTokens=1' : ''
      }`
    ),
};
