import { describe, expect, it } from 'vitest';
import type { DashboardTrafficPoint } from '@/services/api/usageService';
import {
  buildCallsLinePath,
  buildCallsLinePoints,
  buildTrafficAxisTickIndexes,
  buildVisibleTrafficTimeline,
  getTrafficPointX,
  getTrafficMetricShare,
  isCurrentTrafficBucket,
} from './trafficOverviewChartModel';

const point = (hour: number, calls = 0, tokens = 0): DashboardTrafficPoint => ({
  bucket_ms: Date.UTC(2026, 0, 1, hour, 0, 0),
  calls,
  tokens,
  success: calls,
  failure: 0,
  calls_share: calls > 0 ? calls / 10 : 0,
  tokens_share: tokens > 0 ? tokens / 100 : 0,
  failure_rate: 0,
});

describe('trafficOverviewChartModel', () => {
  it('starts visible traffic at the first used bucket and stops at the elapsed bucket', () => {
    const timeline = [
      point(0),
      point(1),
      point(2, 4, 20),
      point(3),
      point(4, 2, 50),
      point(5),
      point(6),
    ];

    expect(buildVisibleTrafficTimeline(timeline, point(5).bucket_ms).map((item) => item.bucket_ms)).toEqual([
      point(2).bucket_ms,
      point(3).bucket_ms,
      point(4).bucket_ms,
      point(5).bucket_ms,
    ]);
  });

  it('keeps elapsed empty buckets when there is no traffic yet', () => {
    const timeline = [point(0), point(1), point(2), point(3)];

    expect(buildVisibleTrafficTimeline(timeline, point(2).bucket_ms)).toHaveLength(3);
  });

  it('builds evenly distributed axis tick indexes', () => {
    expect(buildTrafficAxisTickIndexes(10)).toEqual([0, 2, 4, 5, 7, 9]);
    expect(buildTrafficAxisTickIndexes(3)).toEqual([0, 1, 2]);
  });

  it('clamps metric shares for chart bars', () => {
    const row = { ...point(7, 20, 200), calls_share: 1.8, tokens_share: -0.2 };

    expect(getTrafficMetricShare(row, 'calls')).toBe(1);
    expect(getTrafficMetricShare(row, 'tokens')).toBe(0);
  });

  it('marks only the current elapsed bucket as partial', () => {
    const row = point(12, 2, 10);

    expect(isCurrentTrafficBucket(row, Date.UTC(2026, 0, 1, 12, 30, 0))).toBe(true);
    expect(isCurrentTrafficBucket(row, Date.UTC(2026, 0, 1, 13, 0, 0))).toBe(false);
    expect(isCurrentTrafficBucket(row, null)).toBe(false);
  });

  it('places line points at bucket centers', () => {
    expect(getTrafficPointX(0, 4)).toBe(12.5);
    expect(getTrafficPointX(3, 4)).toBe(87.5);
    expect(getTrafficPointX(0, 1)).toBe(50);
  });

  it('builds a linear calls path without curve commands', () => {
    const rows = [point(7, 2, 10), point(8, 6, 80), point(9, 3, 30)];
    const path = buildCallsLinePath(rows);
    const points = buildCallsLinePoints(rows);

    expect(path).toBe('M 16.667 80 L 50 40 L 83.333 70');
    expect(path).not.toContain('C');
    expect(points.map((item) => item.bucketMs)).toEqual(rows.map((item) => item.bucket_ms));
  });
});
