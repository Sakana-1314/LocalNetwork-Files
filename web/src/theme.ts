import type { GlobalThemeOverrides } from 'naive-ui'

/**
 * 品牌主色：蓝色。
 * 浅色主题沿用 Naive UI 的 info 色系，深色主题用更亮的一档以保证对比度。
 * success / warning / error 等语义色不受影响。
 */
export const BRAND = {
  light: {
    primary: '#2080f0',
    primaryHover: '#4098fc',
    primaryPressed: '#1060c9',
    primarySuppl: '#4098fc',
  },
  dark: {
    primary: '#4098fc',
    primaryHover: '#5aa9ff',
    primaryPressed: '#2b7fd4',
    primarySuppl: '#5aa9ff',
  },
} as const

/**
 * 控件高度：小号提到 34px（与默认 medium 一致），迷你号 28px。
 * 只覆盖交互控件，不动 common.height*，避免 Tag / Skeleton 等被连带放大。
 */
const CONTROL_HEIGHTS = {
  heightTiny: '28px',
  heightSmall: '34px',
}

export function themeOverrides(isDark: boolean): GlobalThemeOverrides {
  const brand = isDark ? BRAND.dark : BRAND.light
  return {
    common: {
      borderRadius: '6px',
      fontFamily:
        '-apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", "Noto Sans CJK SC", sans-serif',
      primaryColor: brand.primary,
      primaryColorHover: brand.primaryHover,
      primaryColorPressed: brand.primaryPressed,
      primaryColorSuppl: brand.primarySuppl,
      ...(isDark ? { bodyColor: '#141417' } : { bodyColor: '#f5f6f8' }),
    },
    Button: { ...CONTROL_HEIGHTS },
    Input: { ...CONTROL_HEIGHTS },
    InternalSelection: { ...CONTROL_HEIGHTS },
    Radio: { buttonHeightSmall: CONTROL_HEIGHTS.heightSmall },
  }
}
