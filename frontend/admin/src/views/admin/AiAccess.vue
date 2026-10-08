<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { notifyError, notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

type Scope = 'catalog:read' | 'inventory:read' | 'report:read'
interface AiKey {
  id: number
  name: string
  key_id: string
  scopes: Scope[]
  created_by: number
  expires_at: string
  revoked_at: string | null
  last_used_at: string | null
  created_at: string
}
interface AiAudit {
  id: number
  key_id: string
  actor_admin_id: number
  action: string
  route: string
  result: string
  created_at: string
}
const translations = {
  'zh-CN': {
    title: 'AI 接入管理', subtitle: '统一管理 OpenClaw、Codex、Claude Code 的独立访问凭证',
    warning: '当前 AI Key 只允许安全读取商品、库存和营业数据，不会授权商品上架、支付配置或退款。密钥仅显示一次，请立即妥善保存。',
    name: '应用名称', example: '例如：NAS OpenClaw / Windows Codex',
    days: '有效期（天，1–90）', scopes: '允许的能力',
    catalog: '商品和分类只读', inventory: '库存预警只读', report: '营业报表只读',
    create: '创建凭证', createConfirm: '确认创建专用 AI Key？完整密钥仅显示一次。',
    refresh: '刷新', rotate: '轮换', rotateConfirm: '旧密钥将立即失效。确定轮换吗？',
    revoke: '撤销', revokeConfirm: '立即撤销此凭证，所有关联的 AI 客户端将无法使用。确定吗？',
    id: '标识', scopeLabel: '权限', status: '状态', expire: '到期', used: '最后使用', action: '操作',
    empty: '暂无 AI 凭证', audit: '最近访问与变更审计', event: '事件', resource: '资源', result: '结果', time: '时间',
    active: '有效', revoked: '已撤销', expired: '已过期',
    tokenTitle: '请立即保存 AI Key', tokenNote: '此窗口关闭后不再显示。请在 NAS/本机的私密凭据文件中使用 DUJIAO_AI_TOKEN，不要发送到聊天、日志或 GitHub。',
    copy: '复制密钥', copied: '已复制到剪贴板', close: '我已安全保存，关闭',
    created: '凭证创建成功', rotated: '已轮换，旧密钥已失效', revokedMsg: '凭证已撤销',
    fail: '操作失败，请检查权限或网络', validation: '请输入名称、1–90 天有效期，并至少勾选一个只读权限',
  },
  'zh-TW': {
    title: 'AI 接入管理', subtitle: '統一管理 OpenClaw、Codex、Claude Code 的獨立存取憑證',
    warning: 'AI Key 目前僅允許安全讀取商品、庫存和營業資料，不授權上架、支付設定或退款。金鑰只顯示一次，請立即安全保存。',
    name: '應用程式名稱', example: '例如：NAS OpenClaw / Windows Codex',
    days: '有效期（天，1–90）', scopes: '允許的能力',
    catalog: '商品與分類唯讀', inventory: '庫存警示唯讀', report: '營業報表唯讀',
    create: '建立憑證', createConfirm: '確定建立 AI Key？完整金鑰只顯示一次。',
    refresh: '重新整理', rotate: '輪換', rotateConfirm: '舊金鑰將立即失效。確定輪換？',
    revoke: '撤銷', revokeConfirm: '立即撤銷後，所有連接的 AI 都無法繼續使用。確定？',
    id: '識別碼', scopeLabel: '權限', status: '狀態', expire: '到期日', used: '上次使用', action: '操作',
    empty: '暫無 AI 憑證', audit: '最近存取與變更稽核', event: '事件', resource: '資源', result: '結果', time: '時間',
    active: '有效', revoked: '已撤銷', expired: '已到期',
    tokenTitle: '請立即儲存 AI Key', tokenNote: '關閉視窗後不再顯示。請在 NAS/本機的私人憑證檔使用 DUJIAO_AI_TOKEN，不要放入聊天、日誌或 GitHub。',
    copy: '複製金鑰', copied: '已複製至剪貼簿', close: '我已安全儲存，關閉',
    created: '憑證建立成功', rotated: '已輪換，舊金鑰已失效', revokedMsg: '憑證已撤銷',
    fail: '操作失敗，請檢查權限或網路', validation: '請輸入名稱、1–90 天有效期並勾選至少一項唯讀權限',
  },
  'en-US': {
    title: 'AI Access Management', subtitle: 'Manage separate credentials for OpenClaw, Codex and Claude Code',
    warning: 'AI Keys allow read-only product, inventory and sales access. They never grant publishing, payment or refund permissions. A new token is shown only once.',
    name: 'Application name', example: 'e.g. NAS OpenClaw / Windows Codex',
    days: 'Lifetime (days, 1–90)', scopes: 'Granted capabilities',
    catalog: 'Read products and categories', inventory: 'Read inventory alerts', report: 'Read sales summaries',
    create: 'Create credential', createConfirm: 'Create an AI Key? The full secret is displayed only once.',
    refresh: 'Refresh', rotate: 'Rotate', rotateConfirm: 'The old secret will stop working immediately. Continue?',
    revoke: 'Revoke', revokeConfirm: 'Revoke this credential immediately for all connected agents?',
    id: 'Identifier', scopeLabel: 'Scopes', status: 'Status', expire: 'Expires', used: 'Last used', action: 'Actions',
    empty: 'No AI credentials yet', audit: 'Recent access and key lifecycle audit', event: 'Event', resource: 'Resource', result: 'Result', time: 'Time',
    active: 'Active', revoked: 'Revoked', expired: 'Expired',
    tokenTitle: 'Save your AI Key now', tokenNote: 'The token cannot be shown again. Use DUJIAO_AI_TOKEN in a private NAS/local secret file. Never put it in chat, logs or GitHub.',
    copy: 'Copy token', copied: 'Copied to clipboard', close: 'I have saved it safely',
    created: 'Credential created', rotated: 'Rotated; old token invalidated', revokedMsg: 'Credential revoked',
    fail: 'Operation failed. Check access and network', validation: 'Enter a name, 1–90 day lifetime and at least one read permission',
  },
} as const

const { locale } = useI18n()
const l = computed(() => translations[(locale.value as keyof typeof translations)] || translations['zh-CN'])
const keys = ref<AiKey[]>([])
const audits = ref<AiAudit[]>([])
const busy = ref(false)
const name = ref('')
const days = ref(30)
const permissions = ref<Scope[]>(['catalog:read', 'inventory:read', 'report:read'])
const justCreatedToken = ref('')
const scopeTypes: Scope[] = ['catalog:read', 'inventory:read', 'report:read']
const keyState = (item: AiKey) =>
  item.revoked_at ? l.value.revoked : new Date(item.expires_at) <= new Date() ? l.value.expired : l.value.active
const formatTime = (value?: string | null) => value ? new Date(value).toLocaleString() : '—'
const scopeLabel = (s: Scope) => s === 'catalog:read' ? l.value.catalog : s === 'inventory:read' ? l.value.inventory : l.value.report

const load = async () => {
  busy.value = true
  try {
    const [one, two] = await Promise.all([adminAPI.listAiKeys(), adminAPI.listAiAudit()])
    keys.value = Array.isArray(one.data?.data) ? one.data.data : []
    audits.value = Array.isArray(two.data?.data) ? two.data.data : []
  } catch (error: any) {
    notifyError(error?.response?.data?.msg || l.value.fail)
  } finally {
    busy.value = false
  }
}
const toggleScope = (scope: Scope, enabled: boolean) => {
  permissions.value = enabled ? [...new Set([...permissions.value, scope])] : permissions.value.filter(v => v !== scope)
}
const create = async () => {
  if (!name.value.trim() || name.value.length > 100 ||
      !Number.isInteger(days.value) || days.value < 1 || days.value > 90 || permissions.value.length === 0) {
    notifyError(l.value.validation); return
  }
  if (!await confirmAction(l.value.createConfirm)) return
  busy.value = true
  try {
    const res = await adminAPI.createAiKey({ name: name.value.trim(), scopes: permissions.value, days: days.value })
    justCreatedToken.value = res.data.data.token || ''
    name.value = ''
    notifySuccess(l.value.created)
    await load()
  } catch (error: any) {
    notifyError(error?.response?.data?.msg || l.value.fail)
  } finally { busy.value = false }
}
const rotate = async (id: number) => {
  if (!await confirmAction(l.value.rotateConfirm)) return
  busy.value = true
  try {
    const res = await adminAPI.rotateAiKey(id)
    justCreatedToken.value = res.data.data.token || ''
    notifySuccess(l.value.rotated)
    await load()
  } catch (error: any) { notifyError(error?.response?.data?.msg || l.value.fail) }
  finally { busy.value = false }
}
const revoke = async (id: number) => {
  if (!await confirmAction(l.value.revokeConfirm)) return
  busy.value = true
  try { await adminAPI.revokeAiKey(id); notifySuccess(l.value.revokedMsg); await load() }
  catch (error: any) { notifyError(error?.response?.data?.msg || l.value.fail) }
  finally { busy.value = false }
}
const copyToken = async () => {
  try { await navigator.clipboard.writeText(justCreatedToken.value); notifySuccess(l.value.copied) }
  catch { notifyError(l.value.fail) }
}
const closeToken = () => { justCreatedToken.value = '' }
onMounted(load)
onBeforeUnmount(closeToken)
</script>

<template>
  <div class="space-y-6 p-5">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">{{ l.title }}</h1>
        <p class="text-sm text-muted-foreground">{{ l.subtitle }}</p>
      </div>
      <Button variant="outline" :disabled="busy" @click="load">{{ l.refresh }}</Button>
    </div>
    <p class="rounded-md border p-4 text-sm text-muted-foreground">{{ l.warning }}</p>

    <Card>
      <CardHeader><CardTitle>{{ l.create }}</CardTitle></CardHeader>
      <CardContent class="space-y-4">
        <div class="grid gap-4 sm:grid-cols-2">
          <label class="space-y-1 text-sm">
            <span>{{ l.name }}</span>
            <Input v-model="name" :placeholder="l.example" maxlength="100" />
          </label>
          <label class="space-y-1 text-sm">
            <span>{{ l.days }}</span>
            <Input v-model.number="days" type="number" min="1" max="90" step="1" />
          </label>
        </div>
        <fieldset class="space-y-2">
          <legend class="text-sm font-medium">{{ l.scopes }}</legend>
          <label v-for="scope in scopeTypes" :key="scope" class="flex gap-2 text-sm">
            <input type="checkbox" :checked="permissions.includes(scope)"
              @change="toggleScope(scope, ($event.target as HTMLInputElement).checked)" />
            <span>{{ scopeLabel(scope) }}</span>
          </label>
        </fieldset>
        <Button :disabled="busy" @click="create">{{ l.create }}</Button>
      </CardContent>
    </Card>

    <Card>
      <CardHeader><CardTitle>{{ l.title }}</CardTitle></CardHeader>
      <CardContent class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead><tr class="border-b text-left">
            <th class="p-3">{{ l.name }}</th><th class="p-3">{{ l.id }}</th>
            <th class="p-3">{{ l.scopeLabel }}</th><th class="p-3">{{ l.status }}</th>
            <th class="p-3">{{ l.expire }}</th><th class="p-3">{{ l.used }}</th><th class="p-3">{{ l.action }}</th>
          </tr></thead>
          <tbody>
            <tr v-for="key in keys" :key="key.id" class="border-b">
              <td class="p-3">{{ key.name }}</td>
              <td class="p-3 font-mono">{{ key.key_id }}</td>
              <td class="p-3">{{ key.scopes.map(scopeLabel).join(' · ') }}</td>
              <td class="p-3">{{ keyState(key) }}</td>
              <td class="p-3">{{ formatTime(key.expires_at) }}</td>
              <td class="p-3">{{ formatTime(key.last_used_at) }}</td>
              <td class="whitespace-nowrap p-3">
                <Button size="sm" variant="outline" :disabled="busy || !!key.revoked_at || new Date(key.expires_at) <= new Date()" @click="rotate(key.id)">{{ l.rotate }}</Button>
                <Button size="sm" variant="destructive" class="ml-2" :disabled="busy || !!key.revoked_at" @click="revoke(key.id)">{{ l.revoke }}</Button>
              </td>
            </tr>
            <tr v-if="keys.length === 0"><td colspan="7" class="p-6 text-center text-muted-foreground">{{ l.empty }}</td></tr>
          </tbody>
        </table>
      </CardContent>
    </Card>

    <Card>
      <CardHeader><CardTitle>{{ l.audit }}</CardTitle></CardHeader>
      <CardContent class="max-h-[360px] overflow-auto">
        <table class="w-full text-sm">
          <thead><tr class="border-b text-left"><th class="p-3">{{ l.id }}</th><th class="p-3">{{ l.event }}</th><th class="p-3">{{ l.resource }}</th><th class="p-3">{{ l.result }}</th><th class="p-3">{{ l.time }}</th></tr></thead>
          <tbody>
            <tr v-for="item in audits" :key="item.id" class="border-b">
              <td class="p-3 font-mono">{{ item.key_id }}</td><td class="p-3">{{ item.action }}</td>
              <td class="p-3">{{ item.route || '—' }}</td><td class="p-3">{{ item.result }}</td>
              <td class="p-3">{{ formatTime(item.created_at) }}</td>
            </tr>
          </tbody>
        </table>
      </CardContent>
    </Card>

    <div v-if="justCreatedToken" role="dialog" aria-modal="true" class="fixed inset-0 z-50 flex items-center justify-center bg-black/70 p-4">
      <div class="w-full max-w-2xl space-y-4 rounded-lg bg-background p-6 shadow-xl">
        <h2 class="text-lg font-semibold">{{ l.tokenTitle }}</h2>
        <p class="text-sm text-muted-foreground">{{ l.tokenNote }}</p>
        <pre class="select-all break-all whitespace-pre-wrap rounded-md border bg-muted p-3 text-sm">{{ justCreatedToken }}</pre>
        <div class="flex flex-wrap gap-2">
          <Button @click="copyToken">{{ l.copy }}</Button>
          <Button variant="outline" @click="closeToken">{{ l.close }}</Button>
        </div>
      </div>
    </div>
  </div>
</template>
