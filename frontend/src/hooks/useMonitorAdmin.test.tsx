// @vitest-environment jsdom
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { act } from 'react';
import { createRoot, type Root } from 'react-dom/client';
import React from 'react';

// 可控的 apiGet mock：每次调用登记 path/signal 与 resolve/reject 句柄，
// 并模拟真实 fetch 语义——signal 中止时以 AbortError 拒绝。
interface PendingRequest {
  path: string;
  signal?: AbortSignal;
  resolve: (v: unknown) => void;
  reject: (e: unknown) => void;
}
const pending = vi.hoisted(() => [] as PendingRequest[]);

const pendingWrites = vi.hoisted(() => [] as PendingRequest[]);

vi.mock('../utils/apiClient', () => ({
  apiGet: vi.fn((path: string, options?: { signal?: AbortSignal }) => {
    return new Promise((resolve, reject) => {
      const entry: PendingRequest = { path, signal: options?.signal, resolve, reject };
      options?.signal?.addEventListener('abort', () => {
        reject(new DOMException('The operation was aborted.', 'AbortError'));
      });
      pending.push(entry);
    });
  }),
  apiPost: vi.fn((path: string) => {
    return new Promise((resolve, reject) => {
      pendingWrites.push({ path, resolve, reject });
    });
  }),
  apiPut: vi.fn(),
  apiDelete: vi.fn(),
  ApiError: class ApiError extends Error {
    status = 500;
  },
}));

import { useMonitorAdmin } from './useMonitorAdmin';

(globalThis as Record<string, unknown>).IS_REACT_ACT_ENVIRONMENT = true;

let hook: ReturnType<typeof useMonitorAdmin>;
function Harness() {
  // eslint-disable-next-line react-hooks/globals -- 测试 harness：把 hook 返回值暴露给用例断言，无并发渲染
  hook = useMonitorAdmin('test-token');
  return null;
}

const listResponse = (n: number) => ({
  monitors: Array.from({ length: n }, (_, i) => ({ key: `m-${i}` })),
  total: n,
});

describe('useMonitorAdmin 列表搜索', () => {
  let root: Root;

  beforeEach(async () => {
    vi.useFakeTimers();
    pending.length = 0;
    pendingWrites.length = 0;
    root = createRoot(document.createElement('div'));
    await act(async () => {
      root.render(React.createElement(Harness));
    });
    // 挂载即取数：先把首次全量请求返回掉，进入稳态
    expect(pending).toHaveLength(1);
    await act(async () => {
      pending[0].resolve(listResponse(92));
    });
  });

  afterEach(async () => {
    await act(async () => {
      root.unmount();
    });
    vi.useRealTimers();
  });

  it('连续键入做防抖：稳定 300ms 后只发一次带最终关键词的请求', async () => {
    await act(async () => { hook.setSearchQuery('s'); });
    await act(async () => { hook.setSearchQuery('ss'); });
    await act(async () => { hook.setSearchQuery('sss'); });
    // 防抖窗口内不应发出任何请求
    expect(pending).toHaveLength(1);

    await act(async () => {
      vi.advanceTimersByTime(300);
    });
    expect(pending).toHaveLength(2);
    expect(pending[1].path).toContain('q=sss');
  });

  it('新请求发出时中止在途旧请求，过期响应不覆盖新结果', async () => {
    // 第一轮搜索 sss，请求在途未返回
    await act(async () => { hook.setSearchQuery('sss'); });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(pending).toHaveLength(2);
    const stale = pending[1];

    // 用户清空关键词 → 第二轮请求发出，旧请求应被中止
    await act(async () => { hook.setSearchQuery(''); });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(pending).toHaveLength(3);
    expect(stale.signal?.aborted).toBe(true);

    // 新请求正常返回
    await act(async () => {
      pending[2].resolve(listResponse(92));
    });
    expect(hook.total).toBe(92);
    expect(hook.isLoading).toBe(false);
    // 被中止的旧请求（已按 AbortError 拒绝）不得改写状态、不得报错
    expect(hook.error).toBeNull();
    expect(hook.total).toBe(92);
  });

  it('被中止的旧请求即使迟到 resolve 也不得覆盖新结果', async () => {
    // 第一轮搜索 sss 在途
    await act(async () => { hook.setSearchQuery('sss'); });
    await act(async () => { vi.advanceTimersByTime(300); });
    const stale = pending[1];

    // 第二轮清空关键词，新请求返回 92 条
    await act(async () => { hook.setSearchQuery(''); });
    await act(async () => { vi.advanceTimersByTime(300); });
    await act(async () => { pending[2].resolve(listResponse(92)); });
    expect(hook.total).toBe(92);

    // 迟到 resolve 已被中止的旧请求（其 promise 已按 AbortError 结算，resolve 无效果）
    await act(async () => { stale.resolve(listResponse(3)); });
    expect(hook.total).toBe(92);
    expect(hook.monitors).toHaveLength(92);
    expect(hook.isLoading).toBe(false);
  });

  it('写操作 await 期间改了搜索词：写后刷新用最新关键词，不得按旧条件落表', async () => {
    // 写请求发出（在途未返回）
    let createDone: Promise<void>;
    await act(async () => {
      createDone = hook.createMonitor({} as never);
    });
    expect(pendingWrites).toHaveLength(1);

    // await 期间用户改搜索词并已发出新搜索
    await act(async () => { hook.setSearchQuery('sss'); });
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(pending).toHaveLength(2);
    expect(pending[1].path).toContain('q=sss');

    // 写请求成功 → 触发的列表刷新必须仍带最新关键词
    await act(async () => {
      pendingWrites[0].resolve({});
      await createDone;
    });
    const last = pending[pending.length - 1];
    expect(last.path).toContain('q=sss');
  });

  it('防抖窗口内点刷新：立即按输入框现值发起搜索，残留 timer 不重复打请求', async () => {
    await act(async () => { hook.setSearchQuery('sss'); });
    // 未等 300ms 就点刷新
    await act(async () => { hook.refreshList(); });
    expect(pending).toHaveLength(2);
    expect(pending[1].path).toContain('q=sss');

    // 原防抖 timer 到期后 set 相同值，不得再打一次
    await act(async () => { vi.advanceTimersByTime(300); });
    expect(pending).toHaveLength(2);
  });

  it('关键词未变时点刷新：直接重拉一次', async () => {
    await act(async () => { hook.refreshList(); });
    expect(pending).toHaveLength(2);
    expect(pending[1].path).not.toContain('q=');
  });

  it('卸载时中止在途列表请求，卸载后返回的写操作不再发起新查询', async () => {
    // 制造一条在途列表请求 + 一条在途写请求
    await act(async () => { hook.setSearchQuery('sss'); });
    await act(async () => { vi.advanceTimersByTime(300); });
    const inflight = pending[1];
    let createDone: Promise<void>;
    await act(async () => {
      createDone = hook.createMonitor({} as never).catch(() => {});
    });

    await act(async () => { root.unmount(); });
    expect(inflight.signal?.aborted).toBe(true);

    // 写请求卸载后才成功：不得再发起新的列表请求
    const before = pending.length;
    await act(async () => {
      pendingWrites[0].resolve({});
      await createDone;
    });
    expect(pending).toHaveLength(before);
  });
});
