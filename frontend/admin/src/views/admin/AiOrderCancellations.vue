<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { RouterLink } from 'vue-router'
import { useI18n } from 'vue-i18n'
import { adminAPI } from '@/api/admin'
import { notifyError, notifySuccess } from '@/utils/notify'
import { confirmAction } from '@/utils/confirm'
import { Button } from '@/components/ui/button'
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card'

type Status='pending'|'executing'|'succeeded'|'rejected'|'conflict'|'failed'
interface Request {
 id:string;key_id:string;order_id:number;order_no:string;currency:string
 expected_total:string;expected_status:string;status:Status
 created_at:string;expires_at:string;failure_code?:string
}
const dict={
 'zh-CN':{
  title:'AI 严格未付款订单取消审批',
  detail:'AI 只能提交申请。你批准后，系统才重新锁定订单并核查所有支付、退款和交付记录。',
  caution:'只能取消没有任何支付尝试（包括成功、失败、过期及删除记录）、没有退款或交付、没有钱包支付、优惠券、子订单及分销关系的未付款订单。其余必须人工处理。这里永远不会退款。',
  refresh:'刷新',none:'暂无取消申请',order:'订单',amount:'原金额',status:'状态',submitted:'申请时间',expires:'到期时间',actions:'操作',
  approve:'批准并取消',reject:'拒绝',open:'打开订单管理',
  confirm:'批准后将尝试取消真实订单。请核对订单编号与金额，系统会重新核对所有严格条件：',
  confirmReject:'确认拒绝此申请？',success:'申请已处理，请核对原订单',rejectedMsg:'申请已拒绝',
  error:'操作失败或订单已变化。请查看原订单，禁止重复执行同一申请。',
  pending:'待批准',executing:'执行中（需要人工检查，不自动重试）',
  succeeded:'已取消',rejected:'已拒绝',conflict:'不符合严格安全条件',failed:'执行失败需核查',expired:'已过期',
 },
 'zh-TW':{
  title:'AI 嚴格未付款訂單取消審批',detail:'AI 只能申請；管理員批准後會再次鎖定訂單核查支付紀錄。',
  caution:'只適用完全未付款且無任何支付嘗試、退款、交付、優惠券、錢包付款、子訂單與分銷的訂單。已刪除或失敗的支付紀錄也會阻擋。此操作絕不退款。',
  refresh:'重新整理',none:'暫無取消申請',order:'訂單',amount:'原金額',status:'狀態',submitted:'提交時間',expires:'到期時間',actions:'操作',
  approve:'批准並取消',reject:'拒絕',open:'開啟訂單管理',
  confirm:'確定嘗試取消真實訂單？請核對訂單與金額：',confirmReject:'確認拒絕？',
  success:'已處理，請檢查訂單',rejectedMsg:'已拒絕',error:'操作失敗或條件變更，請人工核查，不可重複執行。',
  pending:'待批准',executing:'執行中，需人工核查',succeeded:'已取消',
  rejected:'已拒絕',conflict:'不符合條件',failed:'執行失敗',expired:'已到期',
 },
 'en-US':{
  title:'AI Strictly Unpaid Order Cancellation',detail:'The AI only requests cancellation. After your approval, the server locks the order and rechecks all payment and fulfillment records.',
  caution:'Only standalone unpaid orders with NO payment attempt of any status (even deleted), NO refund/fulfillment, wallet paid amount, coupon, referral or child order may be canceled. This NEVER refunds money.',
  refresh:'Refresh',none:'No cancellation requests',order:'Order',amount:'Original amount',status:'Status',submitted:'Requested',expires:'Expires',actions:'Actions',
  approve:'Approve cancellation',reject:'Reject',open:'Open orders',
  confirm:'Approve cancellation of this REAL order after checking ID and amount:',confirmReject:'Reject this request?',
  success:'Action processed. Verify merchant order state.',rejectedMsg:'Rejected',
  error:'Failed or changed order. Manually inspect history. Never retry this request.',
  pending:'Pending',executing:'Executing—manual reconciliation',
  succeeded:'Canceled',rejected:'Rejected',conflict:'Strict criteria failed',failed:'Failed; review',expired:'Expired',
 },
} as const
const {locale}=useI18n()
const t=computed(()=>dict[locale.value as keyof typeof dict]||dict['zh-CN'])
const rows=ref<Request[]>([])
const busy=ref(false)
const working=ref<string|null>(null)
const pending=(v:Request)=>v.status==='pending'&&new Date(v.expires_at)>new Date()
const state=(v:Request)=>v.status==='pending'&&!pending(v)?t.value.expired:t.value[v.status]
const timestamp=(v:string)=>new Date(v).toLocaleString()
const load=async()=>{
 busy.value=true
 try{
  const res=await adminAPI.listAiOrderCancellations()
  rows.value=Array.isArray(res.data?.data)?res.data.data:[]
 }catch{notifyError(t.value.error)}
 finally{busy.value=false}
}
const act=async(v:Request,approve:boolean)=>{
 if(working.value||!pending(v))return
 const message=(approve?t.value.confirm:t.value.confirmReject)+'\n'+v.order_no+
   ' (#'+v.order_id+') · '+v.expected_total+' '+v.currency
 if(!await confirmAction(message))return
 working.value=v.id
 try{
  if(approve)await adminAPI.approveAiOrderCancellation(v.id)
  else await adminAPI.rejectAiOrderCancellation(v.id)
  notifySuccess(approve?t.value.success:t.value.rejectedMsg)
 }catch{notifyError(t.value.error)}
 finally{working.value=null;await load()}
}
onMounted(load)
</script>

<template>
 <div class="space-y-5 p-5">
  <div class="flex flex-wrap items-center justify-between gap-3">
   <div>
    <h1 class="text-2xl font-semibold">{{ t.title }}</h1>
    <p class="text-sm text-muted-foreground">{{ t.detail }}</p>
   </div>
   <Button variant="outline" :disabled="busy||!!working" @click="load">{{ t.refresh }}</Button>
  </div>
  <p class="rounded-lg border p-4 text-sm text-muted-foreground">{{ t.caution }}</p>
  <Card>
   <CardHeader><CardTitle>{{ t.title }}</CardTitle></CardHeader>
   <CardContent class="overflow-x-auto">
    <table class="w-full text-sm">
     <thead><tr class="border-b text-left">
      <th class="p-3">{{ t.order }}</th><th class="p-3">{{ t.amount }}</th>
      <th class="p-3">{{ t.status }}</th><th class="p-3">{{ t.submitted }}</th>
      <th class="p-3">{{ t.expires }}</th><th class="p-3">{{ t.actions }}</th>
     </tr></thead>
     <tbody>
      <tr v-for="v in rows" :key="v.id" class="border-b align-top">
       <td class="p-3">
        <p class="font-mono font-medium">{{ v.order_no }}</p>
        <p class="text-xs text-muted-foreground">#{{ v.order_id }}</p>
       </td>
       <td class="p-3 tabular-nums">{{ v.expected_total }} {{ v.currency }}</td>
       <td class="p-3">{{ state(v) }}
        <p v-if="v.failure_code" class="text-xs text-muted-foreground">{{ v.failure_code }}</p>
       </td>
       <td class="whitespace-nowrap p-3">{{ timestamp(v.created_at) }}</td>
       <td class="whitespace-nowrap p-3">{{ timestamp(v.expires_at) }}</td>
       <td class="p-3">
        <div class="flex flex-wrap gap-2">
         <Button v-if="pending(v)" size="sm" :disabled="!!working" @click="act(v,true)">{{ t.approve }}</Button>
         <Button v-if="pending(v)" size="sm" variant="outline" :disabled="!!working" @click="act(v,false)">{{ t.reject }}</Button>
         <Button size="sm" variant="outline" as-child>
          <RouterLink to="/orders">{{ t.open }}</RouterLink>
         </Button>
        </div>
       </td>
      </tr>
      <tr v-if="rows.length===0">
       <td colspan="6" class="p-6 text-center text-muted-foreground">{{ t.none }}</td>
      </tr>
     </tbody>
    </table>
   </CardContent>
  </Card>
 </div>
</template>
