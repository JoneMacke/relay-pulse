import { useRef, useState, type ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { Info } from 'lucide-react';

interface HeaderInfoPopoverProps {
  /** 浮层内容（解释文字，可含内链） */
  children: ReactNode;
  /** 触发器额外 class（如 ml-1） */
  className?: string;
  /** 浮层宽度 class，默认 w-56 */
  widthClass?: string;
  /** 水平对齐：right=右缘对齐触发器（避免右溢出），center=居中。默认 center */
  align?: 'right' | 'center';
}

/**
 * 表头信息浮层（ⓘ 图标 hover 展开解释文字）。
 *
 * 关键设计：浮层经 createPortal 渲染到 document.body 且 position:fixed，**脱离
 * StatusTable 的 overflow-x:auto 滚动容器**。否则 absolute 浮层向表格下方探出会
 * 撑大滚动容器的 scrollHeight——在单行（嵌入）表上凭空生成竖直滚动条
 * （overflow-x:auto 会令 overflow-y 计算为 auto）。portal 出去后浮层不再是滚动
 * 容器的后代，hover 与否都不影响其 scroll 区。
 *
 * mouseleave 留 120ms 关闭延迟做 hover 桥，让指针能从 ⓘ 移到浮层点击内链。
 * 触发器吞掉 click/keydown，避免连带触发表头列的排序。
 */
export function HeaderInfoPopover({
  children,
  className = '',
  widthClass = 'w-56',
  align = 'center',
}: HeaderInfoPopoverProps) {
  const [coords, setCoords] = useState<{ top: number; left: number } | null>(null);
  const triggerRef = useRef<HTMLSpanElement>(null);
  const closeTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  const open = () => {
    if (closeTimer.current) {
      clearTimeout(closeTimer.current);
      closeTimer.current = null;
    }
    const rect = triggerRef.current?.getBoundingClientRect();
    if (rect) {
      setCoords({
        top: rect.bottom + 4,
        left: align === 'right' ? rect.right : rect.left + rect.width / 2,
      });
    }
  };

  const scheduleClose = () => {
    closeTimer.current = setTimeout(() => setCoords(null), 120);
  };

  return (
    <span
      ref={triggerRef}
      className={`relative inline-flex items-center cursor-help ${className}`}
      onMouseEnter={open}
      onMouseLeave={scheduleClose}
      onClick={(e) => e.stopPropagation()}
      onKeyDown={(e) => e.stopPropagation()}
    >
      <Info size={12} className="text-secondary opacity-70" aria-hidden="true" />
      {coords &&
        createPortal(
          <span
            className={`fixed z-[9999] ${widthClass} rounded-lg border border-default bg-elevated px-2 py-1.5 text-[11px] font-normal normal-case tracking-normal leading-snug whitespace-normal text-primary shadow-lg`}
            style={{
              top: coords.top,
              left: coords.left,
              transform: align === 'right' ? 'translateX(-100%)' : 'translateX(-50%)',
            }}
            onMouseEnter={open}
            onMouseLeave={scheduleClose}
          >
            {children}
          </span>,
          document.body,
        )}
    </span>
  );
}
