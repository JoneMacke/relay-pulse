// @vitest-environment jsdom
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import { describe, it, expect } from 'vitest';
import { AnnotationChip } from './AnnotationChip';
import type { Annotation } from '../../types';

function renderChip(annotation: Annotation): HTMLElement {
  const container = document.createElement('div');
  document.body.appendChild(container);
  act(() => {
    createRoot(container).render(<AnnotationChip annotation={annotation} />);
  });
  return container;
}

describe('AnnotationChip quality_hardfail', () => {
  it('renders an svg icon with aria-label containing label + tooltip', () => {
    const c = renderChip({
      id: 'quality_hardfail',
      family: 'negative',
      icon: 'quality-demote',
      label: '质量移板',
      tooltip: 'opus-4-8 近3次评测均未取得可评分响应，已暂移备用板',
    } as Annotation);
    const img = c.querySelector('[role="img"]');
    expect(img).not.toBeNull();
    expect(img?.getAttribute('aria-label')).toContain('质量移板');
    expect(img?.getAttribute('aria-label')).toContain('已暂移备用板');
    // 断言命中新图标（非 fallback GenericInfoIcon）——靠 data-icon 属性区分。
    expect(c.querySelector('[data-icon="quality-demote"]')).not.toBeNull();
  });
});
