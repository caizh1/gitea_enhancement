<script setup lang="ts">
import {ref, watch} from 'vue';
import GroupNavigationTree from './GroupNavigationTree.vue';
withDefaults(defineProps<{groupId?: number, compact?: boolean}>(), {groupId: 0, compact: false});
const query = ref('');
const refreshKey = ref(0);
watch(query, (value, previous) => { if (!value.trim() && previous.trim()) refreshKey.value++; });
const state = ref('active');
const sort = ref('name');
const direction = ref('asc');
</script>
<template>
  <section>
    <div class="group-navigation-controls">
      <input v-model="query" type="search" class="group-navigation-search" placeholder="搜索群组与仓库…" aria-label="搜索群组与仓库">
      <template v-if="!compact">
        <select v-model="state" aria-label="活动状态"><option value="active">活动</option><option value="inactive">非活动</option></select>
        <select v-model="sort" aria-label="排序字段"><option value="name">名称</option><option value="created">创建时间</option><option value="updated">更新时间</option></select>
        <select v-model="direction" aria-label="排序方向"><option value="asc">升序</option><option value="desc">降序</option></select>
      </template>
    </div>
    <GroupNavigationTree :refresh-key="refreshKey" v-show="!query.trim()" :group-id="groupId" :groups-only="compact" :compact="compact" :state="state" :sort="sort" :direction="direction"/>
    <GroupNavigationTree v-if="query.trim()" :group-id="groupId" :query="query" :groups-only="compact" :compact="compact" :state="state" :sort="sort" :direction="direction"/>
  </section>
</template>
<style scoped>
.group-navigation-controls { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; padding: 16px 0; }
.group-navigation-search { flex: 1 1 180px; min-width: 0; }
.group-navigation-controls input, .group-navigation-controls select { padding: 10px 12px; border: 1px solid var(--color-input-border); background: var(--color-input-background); color: var(--color-text); border-radius: 5px; }
</style>
