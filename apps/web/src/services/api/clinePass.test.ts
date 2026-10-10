import { beforeEach, describe, expect, it, vi } from 'vitest';

const { mocks } = vi.hoisted(() => ({
  mocks: {
    get: vi.fn(),
    post: vi.fn(),
    patch: vi.fn(),
    delete: vi.fn(),
  },
}));

vi.mock('./client', () => ({
  apiClient: mocks,
}));

import { thirdPartyQuotaApi } from './clinePass';

beforeEach(() => {
  Object.values(mocks).forEach((mock) => mock.mockReset());
});

describe('thirdPartyQuotaApi', () => {
  it('uses the dedicated Manager Server account endpoints', async () => {
    mocks.get.mockResolvedValueOnce({ items: [] }).mockResolvedValueOnce({
      accountId: 'account/one',
      fetchedAtMs: 1,
      limits: [],
    });
    mocks.post.mockResolvedValue({ id: 'account-1' });
    mocks.patch.mockResolvedValue({ id: 'account-1' });
    mocks.delete.mockResolvedValue({ ok: true });

    await thirdPartyQuotaApi.list();
    await thirdPartyQuotaApi.create({ provider: 'clinepass', label: 'Work', credential: 'secret' });
    await thirdPartyQuotaApi.update('account/one', { label: 'Personal' });
    await thirdPartyQuotaApi.getUsageLimits('account/one');
    await thirdPartyQuotaApi.delete('account/one');

    expect(mocks.get).toHaveBeenNthCalledWith(1, '/third-party-quota/accounts');
    expect(mocks.post).toHaveBeenCalledWith('/third-party-quota/accounts', {
      provider: 'clinepass',
      label: 'Work',
      credential: 'secret',
    });
    expect(mocks.patch).toHaveBeenCalledWith('/third-party-quota/accounts/account%2Fone', {
      label: 'Personal',
    });
    expect(mocks.get).toHaveBeenNthCalledWith(
      2,
      '/third-party-quota/accounts/account%2Fone/usage-limits'
    );
    expect(mocks.delete).toHaveBeenCalledWith('/third-party-quota/accounts/account%2Fone');
  });

  it('asks the server to re-walk the token total only when forced', async () => {
    mocks.get.mockResolvedValue({ accountId: 'account-1', fetchedAtMs: 1, limits: [] });

    await thirdPartyQuotaApi.getUsageLimits('account-1', { forceTokens: true });
    await thirdPartyQuotaApi.getUsageLimits('account-1', { forceTokens: false });
    await thirdPartyQuotaApi.getUsageLimits('account-1');

    expect(mocks.get).toHaveBeenNthCalledWith(
      1,
      '/third-party-quota/accounts/account-1/usage-limits?forceTokens=1'
    );
    expect(mocks.get).toHaveBeenNthCalledWith(
      2,
      '/third-party-quota/accounts/account-1/usage-limits'
    );
    expect(mocks.get).toHaveBeenNthCalledWith(
      3,
      '/third-party-quota/accounts/account-1/usage-limits'
    );
  });

  it('forwards an explicit credential kind so auto-detection can be overridden', async () => {
    mocks.post.mockResolvedValue({ id: 'account-1' });

    await thirdPartyQuotaApi.create({
      provider: 'clinepass',
      label: 'Work',
      credential: 'sk_cline_key',
      credentialKind: 'api_key',
    });

    expect(mocks.post).toHaveBeenCalledWith('/third-party-quota/accounts', {
      provider: 'clinepass',
      label: 'Work',
      credential: 'sk_cline_key',
      credentialKind: 'api_key',
    });
  });
});
