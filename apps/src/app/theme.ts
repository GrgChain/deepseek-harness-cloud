import type { ThemeConfig } from 'antd';

export const uiColors = {
  primary: '#2563eb', foreground: '#172033', muted: '#526078',
  background: '#fafbff', surface: '#ffffff', border: '#e2e8f0',
  subtle: '#f6f8fc', selected: '#eaf1ff', success: '#15803d', danger: '#dc2626',
  successBackground: '#f0fdf4', successBorder: '#bbf7d0',
  infoBackground: '#eff6ff', infoBorder: '#bfdbfe',
  warningBackground: '#fffbeb', warningBorder: '#fde68a',
  dangerBackground: '#fef2f2', dangerBorder: '#fecaca',
  navigation: '#111c30', navigationText: '#c4cfdf', navigationSelected: '#243653',
} as const;

export const uiFont = '"Host Grotesk", -apple-system, BlinkMacSystemFont, "Segoe UI", "PingFang SC", "Microsoft YaHei", sans-serif';

export const portalTheme: ThemeConfig = {
  token: {
    colorPrimary: uiColors.primary, colorText: uiColors.foreground, colorTextSecondary: uiColors.muted,
    colorBgLayout: uiColors.background, colorBgContainer: uiColors.surface, colorBgElevated: uiColors.surface,
    colorBorder: uiColors.border, colorSuccess: uiColors.success, colorError: uiColors.danger,
    colorSuccessBg: uiColors.successBackground, colorSuccessBorder: uiColors.successBorder,
    colorInfoBg: uiColors.infoBackground, colorInfoBorder: uiColors.infoBorder,
    colorWarningBg: uiColors.warningBackground, colorWarningBorder: uiColors.warningBorder,
    colorErrorBg: uiColors.dangerBackground, colorErrorBorder: uiColors.dangerBorder,
    borderRadius: 8, controlHeight: 40, fontFamily: uiFont, fontSize: 14,
  },
  components: {
    Menu: { itemHeight: 44, itemBorderRadius: 8, darkItemBg: uiColors.navigation,
      darkItemColor: uiColors.navigationText, darkItemSelectedBg: uiColors.navigationSelected,
      darkItemSelectedColor: '#ffffff', darkSubMenuItemBg: uiColors.navigation },
    Table: { headerBg: uiColors.subtle, headerColor: uiColors.muted, cellPaddingBlock: 18,
      borderColor: uiColors.border, rowHoverBg: '#f8faff' },
    Segmented: { trackBg: uiColors.subtle, itemSelectedBg: uiColors.surface, itemSelectedColor: uiColors.primary },
    Button: { primaryShadow: 'none', defaultShadow: 'none', fontWeight: 500 },
    Form: { verticalLabelPadding: '0 0 8px' },
    Drawer: { footerPaddingBlock: 16, footerPaddingInline: 24 },
  },
};
