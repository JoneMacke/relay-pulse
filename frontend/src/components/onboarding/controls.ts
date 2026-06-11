/**
 * 申请向导（onboarding）共享字段样式 —— 单一样式源。
 *
 * 三步表单（服务商信息 / 连通性测试 / 确认）原先把同一串 input / select / label / hint
 * 的 className 逐字重复了十余处，极易在后续维护中漂移成多套视觉。这里收敛成常量，
 * 任一调整改一处即全量生效。
 *
 * 规格为 roomy（px-4 py-2 / rounded-lg / ring-2），面向公开访客的申请流程；后台 admin
 * 的密集表单另有 components/admin/FormControls，待一致性阶段再对齐到本基准。
 */

/**
 * 文本 / URL / 密码输入框（含占位符样式）。
 * error=true 时切到危险色边框，其余情况用默认边框。
 */
export const inputClass = (error = false): string =>
  'w-full px-4 py-2 bg-surface border rounded-lg text-primary placeholder-muted ' +
  'focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50 ' +
  (error ? 'border-danger' : 'border-muted');

/** 下拉选择框（无占位符着色）。 */
export const selectClass =
  'w-full px-4 py-2 bg-surface border border-muted rounded-lg text-primary ' +
  'focus:outline-none focus:ring-2 focus:ring-accent disabled:opacity-50';

/** 字段标签 / fieldset legend。 */
export const labelClass = 'block text-sm font-medium text-primary mb-2';

/** 字段下方的说明 / 提示文字。 */
export const hintClass = 'mt-1 text-xs text-secondary';
