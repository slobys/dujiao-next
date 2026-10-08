<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useRoute } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { notifyError } from '@/utils/notify'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'
import { Button } from '@/components/ui/button'

type Scope = 'catalog:read' | 'inventory:read' | 'report:read' | 'catalog:draft:write' | 'catalog:publish:request' | 'orders:read' | 'orders:review:request' | 'orders:cancel:request'
type Request = {
  id: string
  client_id: string
  client_name: string
  redirect_uri: string
  scopes: Scope[]
  expires_at: string
}
const text = {
  'zh-CN': {
    title: '授权 AI 连接商城', subtitle: '请确认连接的 AI 工具和权限。批准后它只能访问勾选的数据，不会获得管理员登录权限。',
    client: '请求连接的应用', redirect: '连接后将返回', perms: '授予以下只读权限',
    catalog: '查看商品与分类', inventory: '查看库存预警', report: '查看营业汇总',
    draftWrite: '自动创建默认下架的商品草稿（无需每次审批）', publishRequest: '申请商品上架/下架（每次必须人工批准）',
    ordersRead: '读取脱敏订单状态、金额和时间（不含客户信息/卡密）', ordersReview: '提交订单售后审核工单（不直接退款/发货/取消）',
    strictCancel: '申请严格未付款订单取消（必须人工批准；有任何支付记录则拒绝）',
    writeWarning: '商品写入、订单数据及售后申请权限均默认不勾选。仅向可信任的 AI 客户端授权；售后审核不等于执行退款。',
    approve: '批准并连接', cancel: '取消', fail: '连接请求已经过期或无效，请返回 AI 工具重试。',
    none: '至少选择一个权限', risk: '确认这是你主动发起的连接。如果你不认识此客户端或返回地址，请拒绝。',
    loading: '正在检查请求…', done: '已授权，正在返回 AI 工具…',
  },
  'zh-TW': {
    title: '授權 AI 連接商城', subtitle: '請確認 AI 工具及權限。批准後僅可讀取勾選資料，不會取得管理員登入權限。',
    client: '請求連接的應用', redirect: '連接後返回', perms: '授予以下唯讀權限',
    catalog: '查看商品與分類', inventory: '查看庫存警示', report: '查看營業摘要',
    draftWrite: '自動建立未上架商品草稿', publishRequest: '申請上架/下架（每次需人工批准）',
    ordersRead: '讀取去識別化訂單狀態與金額', ordersReview: '提交售後人工跟進工單（不直接退款/出貨）',
    strictCancel: '申請嚴格未付款訂單取消（人工批准；有任何支付紀錄皆拒絕）',
    writeWarning: '商品寫入、訂單資料和售後申請權限均預設不勾選，只向信任的 AI 授權；售後工單不執行退款。',
    approve: '批准並連接', cancel: '取消', fail: '授權要求已過期或無效，請返回 AI 工具重試。',
    none: '請至少選取一項權限', risk: '請確認這是你主動發起的連接。不認識的應用或返回網址應拒絕。',
    loading: '正在檢查請求…', done: '已授權，正在返回 AI 工具…',
  },
  'en-US': {
    title: 'Authorize an AI connection', subtitle: 'Review the client and its permissions. It will not receive an admin JWT or your admin password.',
    client: 'Client requesting access', redirect: 'Return address', perms: 'Grant read-only access',
    catalog: 'Products and categories', inventory: 'Inventory alerts', report: 'Sales summary',
    draftWrite: 'Create unpublished product drafts automatically', publishRequest: 'Request product publish/unpublish (always requires human approval)',
    ordersRead: 'Read sanitized order status and amounts (no customer/private delivery data)', ordersReview: 'Submit after-sales triage tickets (no automatic refund/delivery/cancel)',
    strictCancel: 'Request narrowly restricted unpaid order cancellation (human approval required; any payment record blocks)',
    writeWarning: 'Product write, order data and after-sales review scopes start unchecked. Grant them only to trusted AI clients. Accepting after-sales tickets never initiates refunds.',
    approve: 'Approve and connect', cancel: 'Cancel', fail: 'Authorization expired or invalid. Restart the connection from your AI tool.',
    none: 'Select at least one scope', risk: 'Only approve connections you initiated. Reject unrecognized apps or return addresses.',
    loading: 'Checking request…', done: 'Authorized. Returning to your AI client…',
  },
} as const

const { locale } = useI18n()
const l = computed(() => text[locale.value as keyof typeof text] || text['zh-CN'])
const route = useRoute()
const details = ref<Request | null>(null)
const checked = ref<Scope[]>([])
const busy = ref(true)
const error = ref('')
const complete = ref(false)
const scopeLabel = (scope: Scope) =>
  scope === 'catalog:read' ? l.value.catalog : scope === 'inventory:read' ? l.value.inventory :
  scope === 'report:read' ? l.value.report : scope === 'catalog:draft:write' ? l.value.draftWrite : scope === 'catalog:publish:request' ? l.value.publishRequest : scope === 'orders:read' ? l.value.ordersRead : scope === 'orders:review:request' ? l.value.ordersReview : l.value.strictCancel
const toggle = (scope: Scope, enabled: boolean) => {
  checked.value = enabled ? Array.from(new Set([...checked.value, scope])) : checked.value.filter(v => v !== scope)
}
const fetchRequest = async () => {
  const id = String(route.query.request || '')
  if (!/^[0-9a-f]{32}$/.test(id)) { error.value = l.value.fail; busy.value = false; return }
  try {
    const response = await adminAPI.getAiOAuthRequest(id)
    details.value = response.data?.data || null
    if (!details.value) { error.value = l.value.fail; return }
    // Important: a client's requested write scope NEVER becomes pre-approved.
    checked.value = details.value.scopes.filter(s => s === 'catalog:read' || s === 'inventory:read' || s === 'report:read')
  } catch {
    error.value = l.value.fail
  } finally {
    busy.value = false
  }
}
const approve = async () => {
  if (!details.value || busy.value) return
  if (checked.value.length === 0) { notifyError(l.value.none); return }
  busy.value = true
  try {
    const reply = await adminAPI.approveAiOAuth({
      request_id: details.value.id, scopes: checked.value,
    })
    const next = String(reply.data?.data?.redirect_url || '')
    const parsed = new URL(next)
    if (!(parsed.protocol === 'https:' ||
      (parsed.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(parsed.hostname)))) {
      throw new Error('Invalid redirect')
    }
    complete.value = true
    window.location.assign(next)
  } catch {
    error.value = l.value.fail
    busy.value = false
  }
}
const deny = async () => {
  if (!details.value || busy.value) return
  busy.value = true
  try {
    const res = await adminAPI.denyAiOAuth(details.value.id)
    const target = String(res.data?.data?.redirect_url || '')
    const u = new URL(target)
    if (!(u.protocol === 'https:' || (u.protocol === 'http:' && ['localhost','127.0.0.1','[::1]'].includes(u.hostname)))) throw new Error('invalid callback')
    complete.value = true
    window.location.assign(target)
  } catch { error.value = l.value.fail; busy.value = false }
}
onMounted(fetchRequest)
</script>

<template>
  <div class="mx-auto max-w-2xl space-y-5 p-6">
    <Card>
      <CardHeader>
        <CardTitle>{{ l.title }}</CardTitle>
        <p class="text-sm text-muted-foreground">{{ l.subtitle }}</p>
      </CardHeader>
      <CardContent class="space-y-5">
        <p v-if="busy && !complete" role="status">{{ l.loading }}</p>
        <p v-if="error" role="alert" class="text-sm text-destructive">{{ error }}</p>
        <p v-if="complete" role="status" class="text-sm">{{ l.done }}</p>
        <template v-if="details && !complete">
          <section class="rounded-lg border p-4">
            <h3 class="text-sm font-medium">{{ l.client }}</h3>
            <p class="mt-1 text-lg font-semibold">{{ details.client_name }}</p>
            <p class="font-mono text-xs text-muted-foreground">{{ details.client_id }}</p>
            <h3 class="mt-3 text-sm font-medium">{{ l.redirect }}</h3>
            <p class="mt-1 break-all font-mono text-xs">{{ details.redirect_uri }}</p>
          </section>
          <fieldset class="space-y-3">
            <legend class="text-sm font-medium">{{ l.perms }}</legend>
            <label v-for="scope in details.scopes" :key="scope" class="flex items-center gap-3">
              <input type="checkbox" :checked="checked.includes(scope)"
                @change="toggle(scope, ($event.target as HTMLInputElement).checked)" />
              <span class="text-sm">{{ scopeLabel(scope) }}</span>
            </label>
          </fieldset>
          <p v-if="details.scopes.some(s => s === 'catalog:draft:write' || s === 'catalog:publish:request' || s === 'orders:read' || s === 'orders:review:request' || s === 'orders:cancel:request')" class="rounded-lg border p-3 text-sm text-muted-foreground">{{ l.writeWarning }}</p>
          <p class="rounded-lg border p-3 text-sm text-muted-foreground">{{ l.risk }}</p>
          <div class="flex flex-wrap gap-3">
            <Button :disabled="busy || checked.length === 0" @click="approve">{{ l.approve }}</Button>
            <Button variant="outline" :disabled="busy" @click="deny">{{ l.cancel }}</Button>
          </div>
        </template>
      </CardContent>
    </Card>
  </div>
</template>
