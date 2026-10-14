// 代理内核与连接栈的对应关系，与后端 connectorAllowsKernel 保持一致：
// xray 栈（Xray + sing-box）包含 xray / sing-box，mihomo 栈只包含 mihomo。
// 选择另一栈的内核时，保存会把全局连接栈切换过去（全局只有一套栈，不混用）。
const KERNEL_LABELS: Record<string, string> = {
  xray: 'Xray',
  'sing-box': 'sing-box',
  mihomo: 'Mihomo',
}

export function connectorStackName(connectorType: string): string {
  return connectorType === 'xray' ? 'Xray + sing-box' : 'Mihomo'
}

export function kernelsForConnector(connectorType: string): string[] {
  return connectorType === 'xray' ? ['xray', 'sing-box'] : ['mihomo']
}

export function isKernelAllowedForConnector(kernel: string, connectorType: string): boolean {
  return !kernel || kernel === 'auto' || kernelsForConnector(connectorType).includes(kernel)
}

// connectorForKernel 返回内核所属的连接栈；“自动”不属于任何栈，返回空。
export function connectorForKernel(kernel: string): string {
  if (kernel === 'mihomo') return 'mihomo'
  if (kernel === 'xray' || kernel === 'sing-box') return 'xray'
  return ''
}

export function preferredKernelOptions(): Array<{ value: string; label: string }> {
  return [
    { value: 'auto', label: '自动（推荐，跟随当前连接栈）' },
    ...['xray', 'sing-box', 'mihomo'].map(kernel => ({
      value: kernel,
      label: `${KERNEL_LABELS[kernel]}（${connectorStackName(connectorForKernel(kernel))} 栈）`,
    })),
  ]
}
