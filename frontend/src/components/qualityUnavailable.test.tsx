// @vitest-environment jsdom
//
// D3b unavailable model 守护：rpdiag quality_state="unavailable"（v5.10 stale/aged）
// 的 model 即使 recent_attempts=[]、无历史均值，质量列也要画中性灰 sparkline +
// tooltip 读「不可测」，而**不能**回退成 `-`（那会把"探过但当前不可测"误当"从未测"）。
// 用 react-dom/client 在 jsdom 中真实渲染（零新依赖，沿用 onboardingDisplay.test 形态）。
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect } from 'vitest';
import {
  QualityScoreCell,
  isModelQualityUnusable,
  UNAVAILABLE_COLOR,
  buildModelTooltipRow,
} from './StatusTable';
import type { RpdiagScore, RpdiagModelScore } from '../types/monitor';

function renderCell(score: RpdiagScore): HTMLElement {
  const container = document.createElement('div');
  document.body.appendChild(container);
  const root = createRoot(container);
  act(() => {
    root.render(<QualityScoreCell score={score} />);
  });
  return container;
}

// 纯 unavailable：无 30d/7d 均值、recent_attempts 空、unavailable=true。
const unavailableModel: RpdiagModelScore = {
  model: 'opus-4-8',
  unavailable: true,
  trend: {
    latest: null,
    avg_7d: null,
    avg_30d: null,
    recent_attempts: [],
    n_7d: 0,
    n_30d: 0,
  },
};

describe('unavailable model 质量列渲染', () => {
  it('recent_attempts=[] 的 unavailable model 仍画灰 sparkline，不回退 "-"', () => {
    const container = renderCell({ models: [unavailableModel], trend: unavailableModel.trend, channel_url: '' });
    const svg = container.querySelector('svg');
    // 不是纯 "-" 占位：SVG 存在
    expect(svg).not.toBeNull();
    // 至少一个圆点用中性灰（不可测色）着色
    const greyCircles = Array.from(container.querySelectorAll('circle')).filter(
      (c) => c.getAttribute('fill') === UNAVAILABLE_COLOR,
    );
    expect(greyCircles.length).toBeGreaterThan(0);
    // 文本内容不等于纯 "-"
    expect(container.textContent).not.toBe('-');
  });

  it('isModelQualityUnusable：unavailable 或 failed 均为 true', () => {
    expect(isModelQualityUnusable(unavailableModel)).toBe(true);
    expect(isModelQualityUnusable({ model: 'x', failed: true, trend: unavailableModel.trend })).toBe(true);
    expect(isModelQualityUnusable({ model: 'x', trend: unavailableModel.trend })).toBe(false);
  });

  it('tooltip 近况对 unavailable model 读「不可测」', () => {
    const row = buildModelTooltipRow(unavailableModel);
    expect(row.detail).toContain('不可测');
  });

  it('有历史彩点的 unavailable model：历史彩点保留，不被 every-unavailable 压成整条灰线', () => {
    // 单个数字历史点占住最新槽位 + unavailable：真实近况彩点必须保留（不被误吞成整条灰
    // 线）。此时不再另画灰代表点（不覆盖真实观测值），不可测状态由 tooltip 传达。
    const withHistory: RpdiagModelScore = {
      model: 'sonnet',
      unavailable: true,
      trend: { latest: 88, avg_7d: null, avg_30d: null, recent_attempts: [88], n_7d: 1, n_30d: 0 },
    };
    const container = renderCell({ models: [withHistory], trend: withHistory.trend, channel_url: '' });
    const circles = Array.from(container.querySelectorAll('circle'));
    const colouredCount = circles.filter((c) => c.getAttribute('fill') !== UNAVAILABLE_COLOR).length;
    // 历史彩点 88 保留（未被整条灰线吞掉）；且不是纯 "-"。
    expect(colouredCount).toBeGreaterThan(0);
    expect(container.querySelector('svg')).not.toBeNull();
    // tooltip 仍读到真实近况分（与彩点一致，不被强塞覆盖），但补「当前不可测」后缀，
    // 确保 unavailable 当前态在有历史分时也不消失。
    const row = buildModelTooltipRow(withHistory);
    expect(row.detail).toContain('88');
    expect(row.detail).toContain('当前不可测');
  });
});
