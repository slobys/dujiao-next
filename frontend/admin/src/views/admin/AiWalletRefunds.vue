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
interface Request{
 id:string;key_id:string;order_id:number;order_no:string;currency:string
 expected_total:string;expected_refunded:string;expected_status:string
 amount:string;reason:string;status:Status;created_at:string;expires_at:string
 failure_code?:string;completed_at:string|null
}
const localeText={
 'zh-CN':{
  title:'AI 钱包余额退款审批',subtitle:'AI 只能提出申请。你逐笔批准后，系统才向注册用户的钱包余额实际入账。',
  caution:'重要：批准后将产生真实钱包余额入账，不是支付宝、微信或银行卡原路退款。仅限原订单全额通过钱包付款、人民币、注册用户的独立订单；单次最高 500 元，30 分钟内审批。请核对订单编号、拟退金额、已退金额及原因。异常中断后禁止重复批准。',
  refresh:'刷新',empty:'暂无钱包退款申请',order:'订单',amount:'拟退钱包金额',total:'订单原价 / 已退',reason:'原因',status:'审批状态',expires:'到期',actions:'操作',
  approve:'批准钱包入账',reject:'拒绝',open:'打开订单管理',
  confirm:'确定要向用户钱包实际入账退款吗？原支付渠道不会收到退款。请核对订单号和金额：',
  confirmReject:'确定拒绝这项钱包退款申请？',success:'退款已执行，请核对钱包流水',denied:'退款申请已拒绝',
  error:'申请失效、余额入账失败或订单已改变。请查看审批状态、钱包流水与原订单，不要重复执行。',
  pending:'等待人工批准',executing:'执行中（需要人工核查，不得自动重试）',
  succeeded:'钱包已入账',rejected:'已拒绝',conflict:'资格已变化，未入账',
  failed:'失败，需人工核查',expired:'已过期',
  customer_request:'客户要求',duplicate_purchase:'重复购买',undelivered:'未交付',other:'其他',
 },
 'zh-TW':{
  title:'AI 錢包餘額退款審批',subtitle:'AI 只提交申請。逐筆批准後才會真正為會員錢包入帳。',
  caution:'重要：批准後會實際增加錢包餘額，不是原路退回支付渠道。僅限原本全額以錢包付款的註冊會員人民幣獨立訂單；每次最多 500 元，30 分鐘內審批。請核對金額，禁止重試執行中的申請。',
  refresh:'重新整理',empty:'暫無錢包退款申請',order:'訂單',amount:'退款金額',total:'訂單原價 / 已退',reason:'原因',status:'狀態',expires:'期限',actions:'操作',
  approve:'批准錢包入帳',reject:'拒絕',open:'查看訂單',
  confirm:'確定增加會員錢包餘額？這不是原支付渠道退款。請核對：',
  confirmReject:'確定拒絕？',success:'已入帳，請查核交易記錄',denied:'已拒絕',
  error:'請求失效或交易失敗。請查核帳戶流水，不可重試。',
  pending:'待批准',executing:'執行中，需人工查核',succeeded:'錢包已入帳',
  rejected:'已拒絕',conflict:'訂單已變更',failed:'失敗需查核',expired:'已過期',
  customer_request:'客戶要求',duplicate_purchase:'重複購買',undelivered:'未交付',other:'其他',
 },
 'en-US':{
  title:'AI Wallet Refund Approvals',
  subtitle:'The AI only proposes refunds. Your separate approval credits actual value to the registered customer wallet.',
  caution:'IMPORTANT: Approval credits real WALLET BALANCE, never the original card/Alipay/WeChat payment provider. Only CNY standalone orders originally paid fully by registered-user wallet qualify. Maximum 500 CNY per approval, expires in 30 minutes. Verify order, requested amount, prior refunds and reason. Never retry an executing request.',
  refresh:'Refresh',empty:'No refund proposals',order:'Order',amount:'Wallet credit amount',
  total:'Order total / Already refunded',reason:'Reason',status:'Approval status',expires:'Expires',actions:'Actions',
  approve:'Approve wallet credit',reject:'Reject',open:'Open merchant orders',
  confirm:'Credit REAL wallet funds to this customer? This does NOT refund the original payment provider. Verify order ID and amount:',
  confirmReject:'Reject this AI wallet credit proposal?',success:'Wallet credited—verify ledger',denied:'Request rejected',
  error:'Order changed or action failed. Inspect wallet ledger and request status; NEVER retry an executing request.',
  pending:'Pending approval',executing:'Executing—manual reconciliation required',
  succeeded:'Wallet credited',rejected:'Rejected',conflict:'No longer eligible; not credited',
  failed:'Failed—investigate',expired:'Expired',
  customer_request:'Customer request',duplicate_purchase:'Duplicate purchase',undelivered:'Undelivered',other:'Other',
 }
} as const
const {locale}=useI18n()
const t=computed(()=>localeText[locale.value as keyof typeof localeText]||localeText['zh-CN'])
const rows=ref<Request[]>([])
const busy=ref(false)
const working=ref<string|null>(null)
const canApprove=(v:Request)=>v.status==='pending'&&new Date(v.expires_at)>new Date()
const statusLabel=(v:Request)=>v.status==='pending'&&!canApprove(v)?t.value.expired:t.value[v.status]
const reasonLabel=(v:Request)=>t.value[v.reason as keyof typeof t.value]||v.reason
const date=(v:string)=>new Date(v).toLocaleString()
const fetchRows=async()=>{
 busy.value=true
 try{
  const res=await adminAPI.listAiWalletRefunds()
  rows.value=Array.isArray(res.data?.data)?res.data.data:[]
 }catch{notifyError(t.value.error)}
 finally{busy.value=false}
}
const decide=async(req:Request,approved:boolean)=>{
 if(working.value||!canApprove(req))return
 const caption=approved?t.value.confirm:t.value.confirmReject
 const detail=caption+'\n'+req.order_no+' (#'+req.order_id+') | '+req.amount+' '+req.currency
 if(!await confirmAction(detail))return
 working.value=req.id
 try{
  if(approved)await adminAPI.approveAiWalletRefund(req.id)
  else await adminAPI.rejectAiWalletRefund(req.id)
  notifySuccess(approved?t.value.success:t.value.denied)
 }catch{notifyError(t.value.error)}
 finally{working.value=null;await fetchRows()}
}
onMounted(fetchRows)
</script>

<template>
 <div class="space-y-5 p-5">
  <div class="flex flex-wrap items-center justify-between gap-3">
   <div>
    <h1 class="text-2xl font-semibold">{{ t.title }}</h1>
    <p class="text-sm text-muted-foreground">{{ t.subtitle }}</p>
   </div>
   <Button variant="outline" :disabled="busy||!!working" @click="fetchRows">{{ t.refresh }}</Button>
  </div>
  <p class="rounded-lg border p-4 text-sm text-muted-foreground">{{ t.caution }}</p>
  <Card>
   <CardHeader><CardTitle>{{ t.title }}</CardTitle></CardHeader>
   <CardContent class="overflow-x-auto">
    <table class="w-full text-sm">
     <thead><tr class="border-b text-left">
      <th class="p-3">{{ t.order }}</th>
      <th class="p-3">{{ t.amount }}</th>
      <th class="p-3">{{ t.total }}</th>
      <th class="p-3">{{ t.reason }}</th>
      <th class="p-3">{{ t.status }}</th>
      <th class="p-3">{{ t.expires }}</th>
      <th class="p-3">{{ t.actions }}</th>
     </tr></thead>
     <tbody>
      <tr v-for="v in rows" :key="v.id" class="border-b align-top">
       <td class="p-3">
        <p class="font-mono font-medium">{{ v.order_no }}</p>
        <p class="text-xs text-muted-foreground">#{{ v.order_id }}</p>
       </td>
       <td class="p-3 font-semibold tabular-nums">{{ v.amount }} {{ v.currency }}</td>
       <td class="p-3 tabular-nums">{{ v.expected_total }} / {{ v.expected_refunded }}</td>
       <td class="p-3">{{ reasonLabel(v) }}</td>
       <td class="p-3">
        {{ statusLabel(v) }}
        <p v-if="v.failure_code" class="text-xs text-muted-foreground">{{ v.failure_code }}</p>
       </td>
       <td class="whitespace-nowrap p-3">{{ date(v.expires_at) }}</td>
       <td class="p-3">
        <div class="flex flex-wrap gap-2">
         <Button v-if="canApprove(v)" size="sm" :disabled="!!working" @click="decide(v,true)">{{ t.approve }}</Button>
         <Button v-if="canApprove(v)" size="sm" variant="outline" :disabled="!!working" @click="decide(v,false)">{{ t.reject }}</Button>
         <Button size="sm" variant="outline" as-child>
          <RouterLink to="/orders">{{ t.open }}</RouterLink>
         </Button>
        </div>
       </td>
      </tr>
      <tr v-if="rows.length===0"><td colspan="7" class="p-6 text-center text-muted-foreground">{{ t.empty }}</td></tr>
     </tbody>
    </table>
   </CardContent>
  </Card>
 </div>
</template>
