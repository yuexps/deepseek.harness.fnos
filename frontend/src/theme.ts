import type { GlobalThemeOverrides } from 'naive-ui'

const baseCommon = {
  fontFamily:
    '-apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif',
  borderRadius: '12px',
  borderRadiusSmall: '8px',
  successColor: '#10b981',
  successColorHover: '#34d399',
  successColorPressed: '#059669',
  warningColor: '#f59e0b',
  warningColorHover: '#fbbf24',
  warningColorPressed: '#d97706',
  errorColor: '#ef4444',
  errorColorHover: '#f87171',
  errorColorPressed: '#dc2626',
  infoColor: '#1669ff',
}

export const lightThemeOverrides: GlobalThemeOverrides = {
  common: {
    ...baseCommon,
    primaryColor: '#1669ff',
    primaryColorHover: '#3b82f6',
    primaryColorPressed: '#0052e0',
    primaryColorSuppl: '#1669ff',
    bodyColor: '#f4f6fb',
    cardColor: '#ffffff',
  },
  Card: {
    borderRadius: '16px',
    borderColor: '#f1f5f9',
    boxShadow: '0 1px 3px 0 rgba(0, 0, 0, 0.04), 0 1px 2px -1px rgba(0, 0, 0, 0.04)',
    color: '#ffffff',
  },
  Button: {
    borderRadiusMedium: '10px',
    borderRadiusSmall: '8px',
    fontWeight: '500',
  },
  Input: {
    borderRadius: '10px',
    color: '#f8fafc',
    colorFocus: '#ffffff',
    border: '1px solid #e2e8f0',
  },
  Tag: {
    borderRadius: '9999px',
  },
  Layout: {
    color: '#f5f7fa',
    siderColor: '#ffffff',
    headerColor: '#ffffff',
    footerColor: '#ffffff',
  },
  Menu: {
    borderRadius: '10px',
    itemColorHover: '#f1f5f9',
    itemColorActive: '#eef2ff',
    itemTextColorActive: '#1669ff',
    itemIconColorActive: '#1669ff',
    itemTextColorHover: '#1e293b',
    itemIconColorHover: '#1669ff',
  },
  BackTop: {
    height: '32px',
    width: '32px',
    borderRadius: '16px',
    iconSize: '16px',
  },
}

export const darkThemeOverrides: GlobalThemeOverrides = {
  common: {
    ...baseCommon,
    primaryColor: '#2b7fff',
    primaryColorHover: '#4d94ff',
    primaryColorPressed: '#1669ff',
    primaryColorSuppl: '#2b7fff',
    bodyColor: '#12141a',
    cardColor: '#181b22',
    modalColor: '#1e212b',
    popoverColor: '#1e212b',
    tableColor: '#181b22',
    borderColor: 'rgba(255, 255, 255, 0.08)',
    dividerColor: 'rgba(255, 255, 255, 0.08)',
  },
  Card: {
    borderRadius: '16px',
    color: '#181b22',
    borderColor: 'rgba(255, 255, 255, 0.08)',
    boxShadow: '0 1px 3px 0 rgba(0, 0, 0, 0.3), 0 1px 2px -1px rgba(0, 0, 0, 0.2)',
  },
  Button: {
    borderRadiusMedium: '10px',
    borderRadiusSmall: '8px',
    fontWeight: '500',
  },
  Input: {
    borderRadius: '10px',
    color: '#12141a',
    colorFocus: '#151820',
    border: '1px solid rgba(255, 255, 255, 0.12)',
    borderHover: '1px solid #2b7fff',
    borderFocus: '1px solid #2b7fff',
  },
  Tag: {
    borderRadius: '9999px',
  },
  Layout: {
    color: '#12141a',
    siderColor: '#181b22',
    headerColor: '#181b22',
    footerColor: '#181b22',
  },
  Menu: {
    borderRadius: '10px',
    itemColorHover: 'rgba(255, 255, 255, 0.05)',
    itemColorActive: 'rgba(43, 127, 255, 0.15)',
    itemTextColorActive: '#4d94ff',
    itemIconColorActive: '#4d94ff',
    itemTextColorHover: '#f1f5f9',
    itemIconColorHover: '#4d94ff',
  },
  Switch: {
    railColorActive: '#2b7fff',
  },
  BackTop: {
    height: '32px',
    width: '32px',
    borderRadius: '16px',
    iconSize: '16px',
  },
}

export function getThemeOverrides(theme: 'light' | 'dark'): GlobalThemeOverrides {
  return theme === 'dark' ? darkThemeOverrides : lightThemeOverrides
}

export const themeOverrides = lightThemeOverrides
