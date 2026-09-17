<script setup lang="ts">
import {ref, watch, onBeforeUnmount} from 'vue';
import {SvgIcon} from '../svg.ts';
import {GET} from '../modules/fetch.ts';

type Node = {key: string, id: number, type: string, name: string, full_path: string, url: string, visibility: number, state: string, updated_at: number, expandable: boolean, restricted_navigation: boolean};
const props = withDefaults(defineProps<{refreshKey?: number, groupId?: number, query?: string, state?: string, sort?: string, direction?: string, groupsOnly?: boolean, compact?: boolean}>(), {refreshKey: 0, groupId: 0, query: '', state: 'active', sort: 'name', direction: 'asc', groupsOnly: false, compact: false});
const nodes = ref<Node[]>([]);
const expanded = ref(new Set<string>());
const loading = ref(false);
const error = ref('');
const cursor = ref('');
let controller: AbortController | undefined;
let generation = 0;
let failedAppend = false;
async function load(append = false) {
  controller?.abort();
  controller = new AbortController();
  const current = ++generation;
  loading.value = true;
  error.value = '';
  failedAppend = append;
  const params = new URLSearchParams({q: props.query, state: props.state, sort: props.sort, direction: props.direction, groups_only: String(props.groupsOnly), limit: '20'});
  if (append) params.set('cursor', cursor.value);
  try {
    const response = await GET(`${window.config.appSubUrl}/governance/navigation/groups${props.groupId ? `/${props.groupId}` : ''}?${params}`, {signal: controller.signal});
    if (!response.ok) throw new Error(response.status === 404 || response.status === 403 ? '访问权限已变化，无法查看此内容。' : '加载失败，请重试。');
    if (!response.headers.get('content-type')?.includes('application/json')) throw new Error('登录已失效，请重新登录后重试。');
    const data = await response.json();
    if (current !== generation) return;
    nodes.value = append ? [...nodes.value, ...data.items.filter((node: Node) => !nodes.value.some((old) => old.key === node.key))] : data.items;
    cursor.value = data.next_cursor;
  } catch (cause) {
    if (current !== generation || controller.signal.aborted) return;
    nodes.value = [];
    expanded.value = new Set();
    failedAppend = false;
    error.value = cause instanceof Error ? cause.message : '加载失败，请重试。';
  } finally {
    if (current === generation) loading.value = false;
  }
}
function toggle(key: string) {
  const next = new Set(expanded.value);
  if (next.has(key)) next.delete(key); else next.add(key);
  expanded.value = next;
}
watch(() => props.refreshKey, () => load());
watch(() => [props.groupId, props.query, props.state, props.sort, props.direction, props.groupsOnly], () => {
  nodes.value = [];
  cursor.value = '';
  expanded.value = new Set();
  void load();
}, {immediate: true});
onBeforeUnmount(() => { generation++; controller?.abort(); });
</script>

<template>
  <div class="group-navigation-tree" :class="{compact}" :aria-busy="loading">
    <div v-if="!compact" class="group-navigation-columns muted" aria-hidden="true"><span>名称</span><span>类型</span><span>可见性</span><span>更新时间</span></div>
    <ul class="group-navigation-list">
      <li v-for="node in nodes" :key="node.key">
        <div class="group-navigation-row">
          <div class="group-navigation-name">
            <button v-if="node.expandable && !query" type="button" class="group-navigation-toggle" :aria-label="`${expanded.has(node.key) ? '折叠' : '展开'} ${node.name}`" :aria-expanded="expanded.has(node.key)" @click="toggle(node.key)"><svg-icon :name="expanded.has(node.key) ? 'octicon-chevron-down' : 'octicon-chevron-right'" :size="16"/></button>
            <span v-else class="group-navigation-spacer"/>
            <svg-icon :name="node.type === 'group' ? 'octicon-organization' : 'octicon-repo'" :size="compact ? 22 : 20"/>
            <a :href="node.url" class="group-navigation-link"><strong>{{ node.name }}</strong><small class="muted">{{ node.full_path }}</small></a>
          </div>
          <span v-if="!compact">{{ node.type === 'group' ? '群组' : '仓库' }}</span>
          <span v-if="!compact">{{ node.visibility < 0 ? '受限导航' : ['公开', '内部', '私有'][node.visibility] }}</span>
          <span v-if="!compact" class="muted">{{ node.updated_at ? new Date(node.updated_at * 1000).toLocaleDateString() : '' }}</span>
          <span v-if="node.state !== 'active'" class="ui tiny label">{{ node.state === 'archived' ? '已归档' : '待删除' }}</span>
        </div>
        <div v-if="expanded.has(node.key)" class="group-navigation-children"><GroupNavigationTree :refresh-key="refreshKey" :group-id="node.id" :state="state" :sort="sort" :direction="direction" :groups-only="groupsOnly" :compact="compact"/></div>
      </li>
    </ul>
    <div v-if="error" role="alert" class="ui negative message">{{ error }} <button type="button" class="ui small button" @click="load(failedAppend)">重试</button></div>
    <p v-else-if="!loading && !nodes.length" class="muted tw-p-4">{{ query ? '没有匹配的结果' : '暂无可查看的内容' }}</p>
    <p v-if="loading" role="status" class="muted tw-p-4">正在加载…</p>
    <button v-else-if="cursor && !error" type="button" class="ui basic fluid button" @click="load(true)">加载更多</button>
  </div>
</template>

<style scoped>
.group-navigation-list { list-style: none; margin: 0; padding: 0; }
.group-navigation-row, .group-navigation-columns { display: grid; grid-template-columns: minmax(180px, 1fr) 70px 85px 110px; align-items: center; gap: 12px; padding: 16px 12px; border-bottom: 1px solid var(--color-secondary); }
.group-navigation-name { display: flex; align-items: center; gap: 12px; min-width: 0; }
.group-navigation-name > :deep(svg) { flex: 0 0 auto; }
.group-navigation-toggle, .group-navigation-spacer { display: inline-flex; align-items: center; justify-content: center; flex: 0 0 24px; width: 24px; height: 28px; }
.group-navigation-toggle { background: none; color: inherit; border: 0; cursor: pointer; border-radius: 4px; }
.group-navigation-toggle:hover { background: var(--color-hover); }
.group-navigation-toggle:focus-visible { outline: 2px solid var(--color-primary); }
.group-navigation-link { display: flex; flex-direction: column; min-width: 0; gap: 6px; color: var(--color-text); overflow-wrap: anywhere; }
.group-navigation-link small { font-size: 12px; }
.group-navigation-children { padding-left: 24px; border-left: 1px solid var(--color-secondary); margin-left: 24px; }
.group-navigation-children :deep(.group-navigation-columns) { display: none; }
.compact .group-navigation-row { display: flex; border: 0; padding: 14px 0; }
.compact .group-navigation-link small { font-size: 13px; }
@media (max-width: 640px) {
  .group-navigation-row { grid-template-columns: minmax(0, 1fr); gap: 6px; }
  .group-navigation-columns { display: none; }
  .group-navigation-children { padding-left: 8px; margin-left: 8px; }
}
</style>
