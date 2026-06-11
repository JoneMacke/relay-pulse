/**
 * 后台表单字段的统一「设计语言」—— 单一样式源。
 *
 * admin 原先有三套字段视觉：FormControls（独立表单）、MonitorDetail 的内联编辑控件、
 * 列表过滤框，焦点处理（ring vs 仅 border）、圆角、背景、错误色各不相同。这里把
 * **设计语言**（圆角 rounded-md / 焦点 ring-1+accent / 背景 bg-elevated / 错误 danger）
 * 收敛成单一源；**密度按上下文保留**——独立表单用 px-3 py-2，密集内联编辑网格用 px-2 py-1，
 * 由 dense 形参区分（符合 data-dense 后台原则：编辑 20 字段不因统一而变高）。
 */

interface FieldOpts {
  /** true → 密集内联编辑（px-2 py-1）；默认 false → 独立表单（px-3 py-2）。 */
  dense?: boolean;
  /** true → 错误态危险色边框/焦点环。 */
  error?: boolean;
}

/** 形状 + 焦点：圆角、背景、边框、ring 焦点环、过渡。密度由 dense 决定。 */
const shape = (dense?: boolean): string =>
  `${dense ? 'px-2 py-1' : 'px-3 py-2'} bg-elevated border rounded-md text-primary text-sm ` +
  'focus:outline-none focus:ring-1 transition-colors';

/** 边框/焦点环的语义色：常态 accent，错误态 danger。 */
const state = (error?: boolean): string =>
  error
    ? 'border-danger focus:border-danger focus:ring-danger'
    : 'border-default focus:border-accent focus:ring-accent';

/** 文本/数字/URL/密码输入框（占满宽度，含占位符着色）。 */
export const fieldInputClass = (o: FieldOpts = {}): string =>
  `w-full ${shape(o.dense)} ${state(o.error)} placeholder:text-muted`;

/** 下拉选择框（占满宽度，去原生箭头）。 */
export const fieldSelectClass = (o: FieldOpts = {}): string =>
  `w-full ${shape(o.dense)} ${state(o.error)} appearance-none`;

/** 不含宽度约束的字段语言，供需要自定义宽度的场景组合（如 flex-1 的密钥输入框）。 */
export const fieldShapeClass = (o: FieldOpts = {}): string =>
  `${shape(o.dense)} ${state(o.error)}`;
