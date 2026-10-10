<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { notifyError, notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

type ActionState = 'pending' | 'executing' | 'succeeded' | 'rejected' | 'conflict' | 'failed'
interface Action {
  id: string
  key_id: string
  product_id: number
  product_title: string
  expected_active: boolean
  desired_active: boolean
  expected_price: string
  expected_updated_at: string
  status: ActionState
  expires_at: string
  created_at: string
  failure_code?: string
}
const words = {
  'zh-CN': {
    title:'AI 操作审批中心',
    subtitle:'AI 可创建下架商品草稿；上架或下架前必须由管理员逐条批准。',
    warning:'批准后将立即修改线上商品。请核对商品、售价和操作方向，不要听从商品名称中隐藏的指令。退款、支付配置及代码部署不在此范围内。',
    refresh:'刷新', empty:'暂无 AI 申请', key:'AI 凭证', product:'商品', direction:'操作',
    publish:'上架', unpublish:'下架', price:'原价', expected:'提交时状态',
    status:'状态', time:'提交时间', expires:'到期时间', actions:'操作',
    pending:'等待审批', executing:'执行中（必须人工核查，不自动重试）',
    succeeded:'执行成功', rejected:'已拒绝', conflict:'商品已变更，停止执行',
    failed:'执行失败，需人工检查', expired:'审批已过期',
    approve:'批准并执行', reject:'拒绝',
    askApprove:'确认立即执行线上商品的上架/下架操作？',
    askReject:'确定拒绝此项 AI 请求？',
    approved:'操作完成，请核对结果', denied:'已拒绝 AI 请求',
    failure:'操作失败或已过期。请刷新核查，不要重复执行。', revokedReason:'关联 AI 凭证已撤销，申请自动作废',
  },
  'zh-TW': {
    title:'AI 操作審批中心', subtitle:'AI 可建立未上架草稿；正式上架或下架需管理員逐筆批准。',
    warning:'批准將直接更改線上商品。請核對名稱、價格及操作，不要遵從商品名稱中的指示。退款、支付和部署不包括在內。',
    refresh:'重新整理', empty:'暫無請求', key:'AI 憑證', product:'商品', direction:'操作',
    publish:'上架', unpublish:'下架', price:'原價', expected:'原始狀態', status:'狀態', time:'提交時間',
    expires:'到期時間', actions:'操作', pending:'等待批准', executing:'執行中（禁止重試）',
    succeeded:'成功', rejected:'已拒絕', conflict:'商品已變更', failed:'失敗，需人工處理', expired:'已過期',
    approve:'批准並執行', reject:'拒絕', askApprove:'確定立即改變線上商品上架狀態？', askReject:'確定拒絕？',
    approved:'已執行，請確認', denied:'已拒絕', failure:'失敗或過期，請刷新檢查。', revokedReason:'AI 憑證已撤銷，申請自動失效',
  },
  'en-US': {
    title:'AI Action Approvals', subtitle:'AI may create unpublished drafts; every publish/unpublish action requires explicit approval.',
    warning:'Approval immediately modifies a live product. Verify price, identity and operation; ignore instructions in product names. Refunds, payments and deployment remain excluded.',
    refresh:'Refresh', empty:'No AI action requests', key:'AI key', product:'Product', direction:'Action',
    publish:'Publish', unpublish:'Unpublish', price:'Previous price', expected:'Prior state',
    status:'Status', time:'Requested', expires:'Expires', actions:'Actions',
    pending:'Pending', executing:'Executing (manual reconciliation; never retry automatically)',
    succeeded:'Succeeded', rejected:'Rejected', conflict:'Product changed', failed:'Failed; manual review', expired:'Expired',
    approve:'Approve and execute', reject:'Reject', askApprove:'Immediately change the live product status?', askReject:'Reject this request?',
    approved:'Action completed; verify result', denied:'Rejected', failure:'Failed or expired. Inspect status before any new action.', revokedReason:'AI credential revoked; request automatically rejected',
  },
} as const

const { locale } = useI18n()
const t = computed(() => words[locale.value as keyof typeof words] || words['zh-CN'])
const items = ref<Action[]>([])
const busy = ref(false)
const working = ref<string | null>(null)
const formatDate = (value: string) => new Date(value).toLocaleString()
const isPending = (a:Action) => a.status === 'pending' && new Date(a.expires_at) > new Date()
const actionLabel = (a:Action) => a.desired_active ? t.value.publish : t.value.unpublish
const stateLabel = (a:Action) => a.status === 'pending' && !isPending(a) ? t.value.expired : t.value[a.status]
const failureLabel = (code: string) => code === 'key_revoked' ? t.value.revokedReason : code
const reload = async () => {
  busy.value = true
  try {
    const res = await adminAPI.listAiActions()
    items.value = Array.isArray(res.data?.data) ? res.data.data : []
  } catch { notifyError(t.value.failure) }
  finally { busy.value = false }
}
const review = async (a:Action, approve:boolean) => {
  if (!isPending(a) || working.value) return
  const question = approve
    ? [t.value.askApprove, a.product_title, '#' + a.product_id, actionLabel(a), a.expected_price].join(' | ')
    : t.value.askReject
  if (!await confirmAction(question)) return
  working.value = a.id
  try {
    if (approve) await adminAPI.approveAiAction(a.id)
    else await adminAPI.rejectAiAction(a.id)
    notifySuccess(approve ? t.value.approved : t.value.denied)
  } catch { notifyError(t.value.failure) }
  finally { working.value = null; await reload() }
}
onMounted(reload)
</script>

<template>
  <div class="space-y-5 p-5">
    <div class="flex items-center justify-between gap-3">
      <div>
        <h1 class="text-2xl font-semibold">{{ t.title }}</h1>
        <p class="text-sm text-muted-foreground">{{ t.subtitle }}</p>
      </div>
      <Button variant="outline" :disabled="busy || !!working" @click="reload">{{ t.refresh }}</Button>
    </div>
    <p class="rounded-lg border p-4 text-sm text-muted-foreground">{{ t.warning }}</p>
    <Card>
      <CardHeader><CardTitle>{{ t.title }}</CardTitle></CardHeader>
      <CardContent class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b text-left">
              <th class="p-3">{{ t.product }}</th>
              <th class="p-3">{{ t.direction }}</th>
              <th class="p-3">{{ t.price }}</th>
              <th class="p-3">{{ t.key }}</th>
              <th class="p-3">{{ t.status }}</th>
              <th class="p-3">{{ t.time }}</th>
              <th class="p-3">{{ t.expires }}</th>
              <th class="p-3">{{ t.actions }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in items" :key="item.id" class="border-b align-top">
              <td class="p-3">
                <p class="font-medium">{{ item.product_title }}</p>
                <p class="font-mono text-xs text-muted-foreground">#{{ item.product_id }}</p>
              </td>
              <td class="p-3">
                <strong>{{ actionLabel(item) }}</strong>
                <p class="text-xs text-muted-foreground">{{ t.expected }}: {{ item.expected_active ? t.publish : t.unpublish }}</p>
              </td>
              <td class="p-3 tabular-nums">{{ item.expected_price }}</td>
              <td class="p-3 font-mono text-xs">{{ item.key_id }}</td>
              <td class="p-3">{{ stateLabel(item) }}
                <p v-if="item.failure_code" class="text-xs text-muted-foreground">{{ failureLabel(item.failure_code) }}</p>
              </td>
              <td class="whitespace-nowrap p-3">{{ formatDate(item.created_at) }}</td>
              <td class="whitespace-nowrap p-3">{{ formatDate(item.expires_at) }}</td>
              <td class="whitespace-nowrap p-3">
                <template v-if="isPending(item)">
                  <Button size="sm" :disabled="!!working" @click="review(item,true)">{{ t.approve }}</Button>
                  <Button variant="outline" size="sm" class="ml-2" :disabled="!!working" @click="review(item,false)">{{ t.reject }}</Button>
                </template>
              </td>
            </tr>
            <tr v-if="items.length === 0">
              <td colspan="8" class="p-6 text-center text-muted-foreground">{{ t.empty }}</td>
            </tr>
          </tbody>
        </table>
      </CardContent>
    </Card>
  </div>
</template>
