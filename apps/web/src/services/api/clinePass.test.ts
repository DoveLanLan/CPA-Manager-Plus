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

import { clinePassApi } from './clinePass';

beforeEach(() => {
  Object.values(mocks).forEach((mock) => mock.mockReset());
});

describe('clinePassApi', () => {
  it('uses the dedicated Manager Server account endpoints', async () => {
    mocks.get.mockResolvedValueOnce({ items: [] }).mockResolvedValueOnce({
      accountId: 'account/one',
      fetchedAtMs: 1,
      limits: [],
    });
    mocks.post.mockResolvedValue({ id: 'account-1' });
    mocks.patch.mockResolvedValue({ id: 'account-1' });
    mocks.delete.mockResolvedValue({ ok: true });

    await clinePassApi.list();
    await clinePassApi.create({ label: 'Work', sessionId: 'secret' });
    await clinePassApi.update('account/one', { label: 'Personal' });
    await clinePassApi.getUsageLimits('account/one');
    await clinePassApi.delete('account/one');

    expect(mocks.get).toHaveBeenNthCalledWith(1, '/clinepass/accounts');
    expect(mocks.post).toHaveBeenCalledWith('/clinepass/accounts', {
      label: 'Work',
      sessionId: 'secret',
    });
    expect(mocks.patch).toHaveBeenCalledWith('/clinepass/accounts/account%2Fone', {
      label: 'Personal',
    });
    expect(mocks.get).toHaveBeenNthCalledWith(
      2,
      '/clinepass/accounts/account%2Fone/usage-limits'
    );
    expect(mocks.delete).toHaveBeenCalledWith('/clinepass/accounts/account%2Fone');
  });
});
