// 与后端 config.NormalizeBrowserConnectorType 对应：只有 xray（Xray + sing-box 组合栈）和 mihomo 两套连接栈。
export const CONNECTOR_STACK_OPTIONS = [
  { value: 'mihomo', label: 'Mihomo（默认）' },
  { value: 'xray', label: 'Xray + sing-box' },
]

export function connectorStackLabel(value: string | undefined): string {
  return value === 'xray' ? 'Xray + sing-box' : 'Mihomo'
}
