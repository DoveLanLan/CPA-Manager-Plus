import { Profiler, useEffect } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, describe, expect, it, vi } from 'vitest';
import type { ApiKeyAlias } from '@/services/api/usageService';
import type { ModelPrice } from '@/utils/usage';

vi.mock('../services/monitoringMetaService', () => ({
  loadMonitoringMetaPayload: vi.fn(async () => ({
    authFiles: [],
    channels: [],
    error: '',
  })),
}));

vi.mock('./useMonitoringAnalytics', () => ({
  useMonitoringAnalytics: () => ({
    enabled: true,
    loading: false,
    error: '',
    data: null,
    dataStale: false,
    lastRefreshedAt: null,
    serviceBase: 'http://manager.local',
    unavailableReason: '',
    refresh: vi.fn(),
  }),
}));

import { loadMonitoringMetaPayload } from '../services/monitoringMetaService';
import { useMonitoringData } from './useMonitoringData';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const EMPTY_MODEL_PRICES: Record<string, ModelPrice> = {};
const EMPTY_API_KEY_ALIASES: ApiKeyAlias[] = [];
const ALL_SCOPE_FILTERS = {
  account: 'all',
  provider: 'all',
  model: 'all',
  channel: 'all',
  apiKeyHash: 'all',
  status: 'all',
} as const;

type MonitoringDataResult = ReturnType<typeof useMonitoringData>;

function MonitoringDataHarness({
  onResult,
}: {
  onResult?: (result: MonitoringDataResult) => void;
}) {
  const result = useMonitoringData({
    config: null,
    modelPrices: EMPTY_MODEL_PRICES,
    apiKeyAliases: EMPTY_API_KEY_ALIASES,
    timeRange: 'today',
    customTimeRange: null,
    searchQuery: '',
    searchApiKeyHash: '',
    scopeFilters: ALL_SCOPE_FILTERS,
  });

  useEffect(() => {
    onResult?.(result);
  }, [onResult, result]);

  return null;
}

describe('useMonitoringData render stability', () => {
  let renderer: ReactTestRenderer | null = null;

  afterEach(() => {
    renderer?.unmount();
    renderer = null;
    vi.clearAllMocks();
  });

  it('settles while analytics events are still waiting for the first page', async () => {
    let renderCount = 0;

    await act(async () => {
      renderer = create(
        <Profiler id="monitoring-data" onRender={() => {
          renderCount += 1;
        }}>
          <MonitoringDataHarness />
        </Profiler>
      );
      await new Promise((resolve) => setTimeout(resolve, 20));
    });

    expect(renderCount).toBeLessThan(10);
  });

  it('refreshes analytics without reloading metadata on lightweight refresh', async () => {
    let hookResult: MonitoringDataResult | null = null;

    await act(async () => {
      renderer = create(
        <MonitoringDataHarness
          onResult={(result) => {
            hookResult = result;
          }}
        />
      );
      await new Promise((resolve) => setTimeout(resolve, 20));
    });

    const metadataLoadCalls = vi.mocked(loadMonitoringMetaPayload).mock.calls.length;

    await act(async () => {
      await hookResult?.refreshMeta({
        showLoading: false,
        forceAnalyticsRefresh: false,
        refreshMetadata: false,
      });
    });

    expect(loadMonitoringMetaPayload).toHaveBeenCalledTimes(metadataLoadCalls);
  });
});
