import { useEffect, useState } from 'react'
import { Save } from 'lucide-react'

import { Button, Card, FormItem, Input, Switch, toast } from '../../../shared/components'

import {
  fetchUpstreamProxySettings,
  saveUpstreamProxySettings,
  testUpstreamProxy,
  type UpstreamProxySettings,
  type UpstreamProxyTestResult,
} from '../api'

const URL_PATTERN = /^(http|socks5):\/\/([^@\s]+@)?[^:\s/]+:\d{1,5}\/?$/i

export function UpstreamProxyCard() {
  const [saved, setSaved] = useState<UpstreamProxySettings>({ enabled: false, url: '' })
  const [draft, setDraft] = useState<UpstreamProxySettings>({ enabled: false, url: '' })
  const [saving, setSaving] = useState(false)
  const [testing, setTesting] = useState(false)
  const [testResult, setTestResult] = useState<UpstreamProxyTestResult | null>(null)

  useEffect(() => {
    void fetchUpstreamProxySettings().then(settings => {
      setSaved(settings)
      setDraft(settings)
    })
  }, [])

  const url = draft.url.trim()
  const urlInvalid = url !== '' && !URL_PATTERN.test(url)
  const dirty = draft.enabled !== saved.enabled || url !== saved.url.trim()
  const canSave = dirty && !urlInvalid && (!draft.enabled || url !== '')

  const handleTest = async () => {
    setTesting(true)
    setTestResult(null)
    try {
      setTestResult(await testUpstreamProxy(url))
    } finally {
      setTesting(false)
    }
  }

  const handleSave = async () => {
    setSaving(true)
    try {
      const next = { enabled: draft.enabled, url }
      await saveUpstreamProxySettings(next)
      setSaved(next)
      setDraft(next)
      toast.success(next.enabled ? '前置代理已启用，新启动的实例生效' : '前置代理已关闭')
    } catch (error: any) {
      toast.error(error?.message || String(error) || '保存失败')
    } finally {
      setSaving(false)
    }
  }

  return (
    <Card title="前置代理">
      <div className="space-y-4">
        <div className="flex items-center justify-between rounded-lg border border-[var(--color-border-default)] px-3 py-2">
          <div>
            <p className="text-sm text-[var(--color-text-primary)]">所有连接经过前置代理</p>
            <p className="mt-0.5 text-xs text-[var(--color-text-muted)]">
              包括浏览器实例、Xray / sing-box / Mihomo 连接节点，以及下载、测速等应用请求；连接失败不会回退直连。
            </p>
          </div>
          <Switch
            checked={draft.enabled}
            onChange={enabled => setDraft(prev => ({ ...prev, enabled }))}
            aria-label="启用前置代理"
          />
        </div>

        <FormItem
          label="代理地址"
          required={draft.enabled}
          hint="仅支持 HTTP 与 SOCKS5，可带账号密码。运行中的实例需重启后生效。"
          error={urlInvalid ? '格式应为 socks5://[用户名:密码@]主机:端口 或 http://...' : undefined}
        >
          <Input
            value={draft.url}
            onChange={e => {
              setDraft(prev => ({ ...prev, url: e.target.value }))
              setTestResult(null)
            }}
            placeholder="socks5://user:pass@127.0.0.1:1080"
            error={urlInvalid}
            spellCheck={false}
            autoComplete="off"
          />
        </FormItem>

        <div className="flex flex-wrap items-center gap-3">
          <Button variant="secondary" size="sm" onClick={() => { void handleTest() }} loading={testing} disabled={url === '' || urlInvalid}>
            测试连接
          </Button>
          <Button size="sm" onClick={() => { void handleSave() }} loading={saving} disabled={!canSave}>
            <Save className="h-4 w-4" />
            保存
          </Button>
          {testResult && (
            <span className={`text-xs ${testResult.ok ? 'text-green-600' : 'text-red-500'}`}>
              {testResult.ok ? `连接正常 · ${testResult.latencyMs} ms` : `连接失败：${testResult.error}`}
            </span>
          )}
        </div>
      </div>
    </Card>
  )
}
