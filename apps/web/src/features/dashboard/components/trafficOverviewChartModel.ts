import type { DashboardTrafficPoint } from '@/services/api/usageService';

export type TrafficMetric = 'calls' | 'tokens';

export interface TrafficLinePoint {
  bucketMs: number;
  x: number;
  y: number;
}

const hasTraffic = (point: DashboardTrafficPoint) => point.calls > 0 || point.tokens > 0;

const clampShare = (value: number) => Math.max(0, Math.min(1, value));

const formatCoord = (value: number) =>
  Number.isInteger(value) ? String(value) : value.toFixed(3).replace(/\.?0+$/, '');

const findLastElapsedIndex = (timeline: DashboardTrafficPoint[], nowMs?: number | null) => {
  if (typeof nowMs !== 'number' || !Number.isFinite(nowMs)) {
    return timeline.length - 1;
  }

  for (let index = timeline.length - 1; index >= 0; index -= 1) {
    if (timeline[index].bucket_ms <= nowMs) {
      return index;
    }
  }

  return -1;
};

export const buildVisibleTrafficTimeline = (
  timeline: DashboardTrafficPoint[],
  nowMs?: number | null
) => {
  if (timeline.length === 0) {
    return [];
  }

  const lastElapsedIndex = findLastElapsedIndex(timeline, nowMs);
  const endIndex = Math.max(0, lastElapsedIndex);
  const firstDataIndex = timeline.findIndex((point, index) => index <= endIndex && hasTraffic(point));

  if (firstDataIndex < 0) {
    return timeline.slice(0, endIndex + 1);
  }

  return timeline.slice(firstDataIndex, endIndex + 1);
};

export const buildTrafficAxisTickIndexes = (pointCount: number, maxTicks = 6) => {
  if (pointCount <= 0) {
    return [];
  }
  if (pointCount <= maxTicks) {
    return Array.from({ length: pointCount }, (_, index) => index);
  }

  const tickCount = Math.max(2, maxTicks);
  const indexes = new Set<number>();
  for (let index = 0; index < tickCount; index += 1) {
    indexes.add(Math.round((index * (pointCount - 1)) / (tickCount - 1)));
  }

  return Array.from(indexes).sort((left, right) => left - right);
};

export const getTrafficMetricShare = (point: DashboardTrafficPoint, metric: TrafficMetric) =>
  clampShare(metric === 'calls' ? point.calls_share : point.tokens_share);

export const getTrafficPointX = (index: number, pointCount: number) =>
  pointCount <= 0 ? 0 : ((index + 0.5) / pointCount) * 100;

export const buildCallsLinePoints = (timeline: DashboardTrafficPoint[]): TrafficLinePoint[] =>
  timeline.map((point, index) => ({
    bucketMs: point.bucket_ms,
    x: getTrafficPointX(index, timeline.length),
    y: 100 - getTrafficMetricShare(point, 'calls') * 100,
  }));

export const buildCallsLinePath = (timeline: DashboardTrafficPoint[]) =>
  buildCallsLinePoints(timeline)
    .map((point, index) => `${index === 0 ? 'M' : 'L'} ${formatCoord(point.x)} ${formatCoord(point.y)}`)
    .join(' ');

export const isCurrentTrafficBucket = (
  point: DashboardTrafficPoint,
  nowMs?: number | null,
  bucketMs = 60 * 60 * 1000
) =>
  typeof nowMs === 'number' &&
  Number.isFinite(nowMs) &&
  nowMs >= point.bucket_ms &&
  nowMs < point.bucket_ms + bucketMs;
