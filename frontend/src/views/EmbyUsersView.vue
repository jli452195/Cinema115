<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import ManagementLayout from '../components/ManagementLayout.vue'
import MemberPlaybackEditor from '../components/MemberPlaybackEditor.vue'
import { memberRequest, memberStage, type MemberPlayback, type MemberSettings } from '../types/memberPlayback'
import type { P115Account } from '../types/p115Accounts'
const props=defineProps<{token:string}>()
defineEmits<{logout:[]}>()
const members=ref<MemberPlayback[]>([]),accounts=ref<P115Account[]>([])
const settings=ref<MemberSettings>({enabled:false,revision:0,libraryIds:[],allowDownload:false})
const error=ref(''),query=ref(''),filter=ref('all'),selected=ref(''),busy=ref(false),editing=ref(false),dirty=ref(false),confirmDiscard=ref(false)
const lifecycle=new AbortController()
const filtered=computed(()=>members.value.filter(m=>(m.embyName+' '+m.embyId).toLowerCase().includes(query.value.toLowerCase())&&(filter.value==='all'||!m.administrator&&(filter.value==='pending'&&m.stage!=='ready'||m.source===filter.value))))
const group=ref('all')
const grouped=computed(()=>filtered.value.filter(m=>group.value==='all'||(group.value==='excluded')===!!(m.administrator||m.skipLibrarySync)))
const current=computed(()=>members.value.find(m=>m.userId===selected.value && !m.administrator))
async function load(){
 if(busy.value||editing.value)return
 if(dirty.value){confirmDiscard.value=true;return}
 busy.value=true;error.value=''
 try{const [data,pool]=await Promise.all([memberRequest(props.token,lifecycle.signal,'/users'),memberRequest(props.token,lifecycle.signal,'/accounts')]);members.value=data.members;settings.value=data.settings;accounts.value=pool.accounts}
 catch(e){if(!lifecycle.signal.aborted)error.value=e instanceof Error?e.message:'读取失败'}finally{busy.value=false}
}
async function changeGroup(m:MemberPlayback){
 if(busy.value||editing.value||dirty.value||m.administrator)return
 busy.value=true;error.value=''
 try{
  const data=await memberRequest(props.token,lifecycle.signal,`/${encodeURIComponent(m.userId)}/library-group`,{skipLibrarySync:!m.skipLibrarySync})
  const updated=(data.members as MemberPlayback[]).find(x=>x.userId===m.userId)
  if(!updated)throw new Error('用户状态缺失，请刷新检查')
  members.value=members.value.map(x=>x.userId===m.userId?{...x,...updated}:x);settings.value=data.settings
 }catch(e){if(!lifecycle.signal.aborted)error.value=e instanceof Error?e.message:'操作失败'}finally{busy.value=false}
}
function select(id:string){if(dirty.value){confirmDiscard.value=true;return};selected.value=id}
function updated(m:MemberPlayback,s:MemberSettings){members.value=members.value.map(x=>x.userId===m.userId?m:x);settings.value=s;dirty.value=false}
function source(m:MemberPlayback){if(m.administrator)return '管理员账号 · 不参与个人播放授权';return m.source==='personal'?'个人网盘':m.source==='assigned'?(m.assignedAccountId?'小号池 · '+(accounts.value.find(a=>a.id===m.assignedAccountId)?.name||'账号不可用'):'小号池 · 等待分配'):'待授权'}
onMounted(load);onBeforeUnmount(()=>lifecycle.abort())
</script>
<template>
 <ManagementLayout title="Emby 用户管理" description="统一管理播放授权和网盘。用户使用 Emby 账号登录个人中心。" :parent="{label:'Emby',to:'/emby'}" @logout="$emit('logout')">
 <template #actions><RouterLink to="/users" class="text-sm text-blue-600">邀请与播放设置</RouterLink></template>
 <div class="mb-5 flex flex-wrap gap-3"><input v-model="query" aria-label="搜索 Emby 用户" placeholder="搜索 Emby 名称或 ID" class="min-w-0 flex-1 rounded-lg border border-slate-200 bg-white p-3 dark:border-slate-700 dark:bg-slate-900"/><select v-model="filter" aria-label="筛选用户" class="rounded-lg border p-3 dark:border-slate-700 dark:bg-slate-900"><option value="all">全部用户</option><option value="personal">个人网盘</option><option value="assigned">小号池</option><option value="pending">待开通 / 已暂停</option></select><select v-model="group" aria-label="媒体库同步分组" class="rounded-lg border p-3 dark:border-slate-700 dark:bg-slate-900"><option value="all">全部分组</option><option value="default">默认组</option><option value="excluded">不同步组</option></select><button :disabled="busy||editing" class="text-sm text-blue-600 disabled:opacity-40" @click="load">刷新</button></div>
 <p class="mb-4 text-sm text-slate-500">不同步组保留 Emby 中设置的媒体库权限，管理员固定归入此组。移回默认组后会按统一设置覆盖媒体库权限；播放绑定和原文件播放限制继续生效。</p>
 <p v-if="error" role="alert" class="mb-4 rounded-lg bg-red-50 p-3 text-red-700">{{error}}</p>
 <div v-if="confirmDiscard" class="mb-4 rounded-lg bg-amber-50 p-3 text-sm text-amber-800">当前有未保存的修改。<button class="mx-3 underline" @click="dirty=false;selected='';confirmDiscard=false;load()">放弃修改并刷新</button><button @click="confirmDiscard=false">继续编辑</button></div>
 <div class="grid gap-6 lg:grid-cols-2"><section class="space-y-3"><article v-for="m in grouped" :key="m.userId" class="rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900"><div class="flex justify-between gap-3"><div><h2 class="font-semibold">{{m.embyName}}</h2><span v-if="m.administrator" class="mt-1 inline-block rounded-full bg-purple-100 px-2 py-0.5 text-xs text-purple-700 dark:bg-purple-400/10 dark:text-purple-300">管理员</span></div><button v-if="!m.administrator" :disabled="editing" class="text-sm text-blue-600" @click="select(m.userId)">管理</button></div><p class="mt-2 text-sm text-slate-500">{{source(m)}}</p><div class="mt-2 flex flex-wrap items-center gap-3 text-xs"><span class="rounded-full bg-slate-100 px-2 py-1 text-slate-600 dark:bg-slate-800 dark:text-slate-300">{{m.administrator||m.skipLibrarySync?'不同步组':'默认组'}}</span><button v-if="!m.administrator" :disabled="busy||editing||dirty" class="text-blue-600 disabled:opacity-40" @click="changeGroup(m)">{{m.skipLibrarySync?'移回默认组':'加入不同步组'}}</button></div><p class="mt-2 text-sm text-slate-500">{{memberStage(m,settings)}}</p></article><p v-if="!grouped.length" class="p-6 text-sm text-slate-500">{{busy?'正在读取…':'暂无用户。通过邀请注册，或直接使用已有 Emby 账号登录。'}}</p></section>
 <section v-if="current" class="self-start rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-700 dark:bg-slate-900"><h2 class="mb-4 font-semibold">{{current.embyName}}</h2><MemberPlaybackEditor :key="current.userId+':'+settings.revision" :token="token" :member="current" :settings="settings" :accounts="accounts" :disabled="current.disabled" @updated="updated" @busy="editing=$event" @dirty="dirty=$event"/><p class="mt-5 break-all text-xs text-slate-500">Emby ID：{{current.embyId}}</p></section></div>
 </ManagementLayout>
</template>
