<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { notifyError, notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

type State='pending'|'accepted'|'rejected'|'resolved'|'conflict'
interface Ticket {
  id: string
  key_id: string
  order_id: number
  order_no: string
  reason: string
  expected_status: string
  expected_total: string
  currency: string
  status: State
  created_at: string
  expires_at: string
  reviewed_at: string|null
  resolved_at: string|null
}
const translations={
  'zh-CN':{
    title:'AI 订单售后审核',subtitle:'AI 发现异常 → 提交结构化售后工单 → 管理员接收或拒绝 → 在原订单管理页面人工处理。',
    warning:'接收工单不会退款、取消订单或发货！必须进入订单管理核查原始支付、客户和交付信息，在原有业务流程中完成实际操作，处理结束后再标记“已完成跟进”。',
    refresh:'刷新', none:'暂无售后工单', order:'订单', reason:'售后原因', status:'工单状态',
    request:'请求时间', due:'有效期', amount:'订单金额快照', actions:'操作',
    accept:'接收人工处理', reject:'拒绝', resolve:'已完成跟进', open:'打开订单管理',
    askAccept:'确认将这项售后请求接收为人工待办？这不会退款或改变订单状态。',
    askReject:'确定拒绝这项 AI 售后工单？',
    askResolve:'确认你已在原商城订单管理流程中完成实际核查与跟进？此处仅更新工单状态。',
    acceptedMessage:'工单已接收，请进入订单管理手动跟进', rejectedMessage:'工单已拒绝',
    resolvedMessage:'已记录人工跟进完成', fail:'操作失败或订单已变更，请刷新核对，不要重复执行。',
    pending:'待审核', accepted:'待人工跟进', rejected:'已拒绝', resolved:'已标记处理完成', conflict:'订单已发生变化', expired:'已过期',
    refund_review:'退款审核建议', delivery_delay:'交付延迟', payment_exception:'支付异常',
    cancellation_request:'订单取消建议', other_exception:'其他异常需人工核查',
  },
  'zh-TW':{
    title:'AI 訂單售後審核',subtitle:'AI 提出異常 → 建立售後工單 → 管理員人工接受或拒絕 → 原訂單管理中處理。',
    warning:'接受工單並不會退款、取消訂單或發貨。請進入原訂單管理核對支付、客戶與交付資料，操作完成才標記跟進完成。',
    refresh:'重新整理',none:'暫無工單',order:'訂單',reason:'售後原因',status:'工單狀態',
    request:'提交時間',due:'有效期',amount:'金額快照',actions:'操作',
    accept:'接受人工處理',reject:'拒絕',resolve:'已完成跟進',open:'開啟訂單管理',
    askAccept:'確認接受此售後跟進？不會退款或變更訂單。',askReject:'確定拒絕 AI 工單？',
    askResolve:'確認已在原訂單管理完成實際處理？此處只更新工單狀態。',
    acceptedMessage:'已接受，請人工跟進',rejectedMessage:'已拒絕',
    resolvedMessage:'已標記完成',fail:'操作失敗或訂單已變更，請重新整理。',
    pending:'待審核',accepted:'待跟進',rejected:'已拒絕',resolved:'已跟進',conflict:'訂單已變更',expired:'已過期',
    refund_review:'退款審核建議',delivery_delay:'交付延遲',payment_exception:'支付異常',
    cancellation_request:'取消訂單建議',other_exception:'其他異常',
  },
  'en-US':{
    title:'AI Order & After-Sales Reviews',subtitle:'AI proposes a triage ticket; an administrator accepts or rejects it, then handles the order using the native admin workflow.',
    warning:'Accepting a ticket DOES NOT refund, cancel or deliver an order. Investigate and act through the existing merchant order admin. Mark resolved only after you have manually followed up.',
    refresh:'Refresh',none:'No after-sales tickets',order:'Order',reason:'Issue',status:'Ticket status',
    request:'Requested',due:'Expires',amount:'Original amount',actions:'Actions',
    accept:'Accept for manual follow-up',reject:'Reject',resolve:'Mark follow-up completed',open:'Open Orders',
    askAccept:'Accept this triage task for manual review? No refund or order change will occur.',
    askReject:'Reject this AI after-sales request?',
    askResolve:'Confirm you handled this case in the regular order admin? This only closes the AI ticket.',
    acceptedMessage:'Accepted. Follow up in Orders.',rejectedMessage:'Rejected',
    resolvedMessage:'Human follow-up recorded',fail:'Failed or stale order. Refresh; do not execute blindly.',
    pending:'Pending',accepted:'Manual follow-up needed',rejected:'Rejected',resolved:'Follow-up marked completed',conflict:'Order changed',expired:'Expired',
    refund_review:'Refund review suggestion',delivery_delay:'Fulfillment delayed',payment_exception:'Payment exception',
    cancellation_request:'Order cancellation suggestion',other_exception:'Other anomaly',
  },
} as const

const {locale}=useI18n()
const t=computed(()=>translations[locale.value as keyof typeof translations]||translations['zh-CN'])
const tickets=ref<Ticket[]>([])
const busy=ref(false)
const working=ref<string|null>(null)
const isPending=(v:Ticket)=>v.status==='pending'&&new Date(v.expires_at)>new Date()
const labelState=(v:Ticket)=>v.status==='pending'&&!isPending(v)?t.value.expired:t.value[v.status]
const reasonLabel=(v:Ticket)=>t.value[v.reason as keyof typeof t.value]||v.reason
const date=(value:string)=>new Date(value).toLocaleString()
const load=async()=>{
  busy.value=true
  try{
    const response=await adminAPI.listAiOrderReviews()
    tickets.value=Array.isArray(response.data?.data)?response.data.data:[]
  }catch{notifyError(t.value.fail)}
  finally{busy.value=false}
}
const change=async(item:Ticket,action:'accept'|'reject'|'resolve')=>{
  if(working.value)return
  if(action==='resolve'&&item.status!=='accepted')return
  if(action!=='resolve'&&!isPending(item))return
  const question=action==='accept'?t.value.askAccept:action==='reject'?t.value.askReject:t.value.askResolve
  if(!await confirmAction(question))return
  working.value=item.id
  try{
    if(action==='accept')await adminAPI.acceptAiOrderReview(item.id)
    else if(action==='reject')await adminAPI.rejectAiOrderReview(item.id)
    else await adminAPI.resolveAiOrderReview(item.id)
    notifySuccess(action==='accept'?t.value.acceptedMessage:action==='reject'?t.value.rejectedMessage:t.value.resolvedMessage)
  }catch{notifyError(t.value.fail)}
  finally{working.value=null;await load()}
}
onMounted(load)
</script>

<template>
  <div class="space-y-5 p-5">
    <div class="flex items-start justify-between gap-4">
      <div>
        <h1 class="text-2xl font-semibold">{{ t.title }}</h1>
        <p class="text-sm text-muted-foreground">{{ t.subtitle }}</p>
      </div>
      <Button variant="outline" :disabled="busy||!!working" @click="load">{{ t.refresh }}</Button>
    </div>
    <p class="rounded-lg border p-4 text-sm text-muted-foreground">{{ t.warning }}</p>
    <Card>
      <CardHeader><CardTitle>{{ t.title }}</CardTitle></CardHeader>
      <CardContent class="overflow-x-auto">
        <table class="w-full text-sm">
          <thead>
            <tr class="border-b text-left">
              <th class="p-3">{{ t.order }}</th>
              <th class="p-3">{{ t.reason }}</th>
              <th class="p-3">{{ t.amount }}</th>
              <th class="p-3">{{ t.status }}</th>
              <th class="p-3">{{ t.request }}</th>
              <th class="p-3">{{ t.due }}</th>
              <th class="p-3">{{ t.actions }}</th>
            </tr>
          </thead>
          <tbody>
            <tr v-for="item in tickets" :key="item.id" class="border-b align-top">
              <td class="p-3">
                <p class="font-mono font-semibold">{{ item.order_no }}</p>
                <p class="text-xs text-muted-foreground">#{{ item.order_id }}</p>
                <p class="text-xs text-muted-foreground">{{ item.expected_status }}</p>
              </td>
              <td class="p-3">{{ reasonLabel(item) }}</td>
              <td class="p-3 tabular-nums">{{ item.expected_total }} {{ item.currency }}</td>
              <td class="p-3">{{ labelState(item) }}</td>
              <td class="whitespace-nowrap p-3">{{ date(item.created_at) }}</td>
              <td class="whitespace-nowrap p-3">{{ date(item.expires_at) }}</td>
              <td class="p-3">
                <div class="flex flex-wrap gap-2">
                  <Button v-if="isPending(item)" size="sm" :disabled="!!working" @click="change(item,'accept')">{{ t.accept }}</Button>
                  <Button v-if="isPending(item)" size="sm" variant="outline" :disabled="!!working" @click="change(item,'reject')">{{ t.reject }}</Button>
                  <Button v-if="item.status==='accepted'" size="sm" :disabled="!!working" @click="change(item,'resolve')">{{ t.resolve }}</Button>
                  <Button size="sm" variant="outline" as-child><RouterLink to="/orders">{{ t.open }}</RouterLink></Button>
                </div>
              </td>
            </tr>
            <tr v-if="tickets.length===0">
              <td colspan="7" class="p-6 text-center text-muted-foreground">{{ t.none }}</td>
            </tr>
          </tbody>
        </table>
      </CardContent>
    </Card>
  </div>
</template>
