<script setup lang="ts">
import { computed, h } from 'vue'
import { NButton, NDataTable, NIcon, NTag, useThemeVars, type DataTableColumns, type DataTableSortState } from 'naive-ui'
import { EllipsisHorizontal } from '@vicons/ionicons5'
import type { Entry } from '@/api/types'
import FileIcon from '@/components/common/FileIcon.vue'
import { formatDate, formatRelative, formatSize } from '@/utils/format'
import type { SortKey, SortState } from './sort'

const props = defineProps<{
  entries: Entry[]
  selected: string[]
  sort: SortState
  loading?: boolean
  isRoot?: boolean
  compact?: boolean
}>()

const emit = defineEmits<{
  'update:selected': [keys: string[]]
  'update:sort': [s: SortState]
  open: [entry: Entry]
  select: [entry: Entry, ev: MouseEvent]
  contextmenu: [entry: Entry, ev: MouseEvent]
}>()

function sortOrderOf(key: SortKey) {
  return props.sort.key === key ? props.sort.order : false
}

const columns = computed<DataTableColumns<Entry>>(() => {
  const cols: DataTableColumns<Entry> = [
    { type: 'selection', width: 44 },
    {
      title: '名称',
      key: 'name',
      sorter: true,
      sortOrder: sortOrderOf('name'),
      ellipsis: false,
      render: (row) =>
        h('div', { class: 'name-cell' }, [
          h(FileIcon, { entry: row, size: 22, mount: props.isRoot && row.is_dir }),
          h('div', { class: 'name-main' }, [
            h(
              'a',
              {
                class: 'name-link',
                title: row.name,
                onClick: (e: MouseEvent) => {
                  e.stopPropagation()
                  emit('open', row)
                },
              },
              row.name,
            ),
            props.compact
              ? h(
                  'div',
                  { class: 'name-sub' },
                  row.is_dir ? formatRelative(row.mod_time) : `${formatSize(row.size)} · ${formatRelative(row.mod_time)}`,
                )
              : null,
          ]),
          props.isRoot && !row.writable
            ? h(NTag, { size: 'small', bordered: false, class: 'ro-tag' }, { default: () => '只读' })
            : null,
        ]),
    },
  ]
  if (!props.compact) {
    cols.push(
      {
        title: '大小',
        key: 'size',
        width: 110,
        sorter: true,
        sortOrder: sortOrderOf('size'),
        align: 'right',
        render: (row) => h('span', { class: 'muted' }, row.is_dir ? '—' : formatSize(row.size)),
      },
      {
        title: '修改时间',
        key: 'mod_time',
        width: 170,
        sorter: true,
        sortOrder: sortOrderOf('mod_time'),
        render: (row) => h('span', { class: 'muted', title: formatDate(row.mod_time, 'YYYY-MM-DD HH:mm:ss') }, formatRelative(row.mod_time)),
      },
    )
  }
  cols.push({
    key: 'actions',
    width: 48,
    render: (row) =>
      h(
        NButton,
        {
          quaternary: true,
          circle: true,
          size: 'small',
          class: 'more-btn',
          onClick: (e: MouseEvent) => {
            e.stopPropagation()
            emit('contextmenu', row, e)
          },
        },
        { icon: () => h(NIcon, { component: EllipsisHorizontal }) },
      ),
  })
  return cols
})

function onSorter(s: DataTableSortState | DataTableSortState[] | null): void {
  const st = Array.isArray(s) ? s[0] : s
  if (!st || !st.order) {
    emit('update:sort', { key: 'name', order: 'ascend' })
    return
  }
  emit('update:sort', { key: st.columnKey as SortKey, order: st.order })
}

const selectedSet = computed(() => new Set(props.selected))

function rowProps(row: Entry) {
  return {
    class: selectedSet.value.has(row.path) ? 'row-selected' : '',
    style: 'cursor: default',
    onClick: (e: MouseEvent) => {
      const t = e.target as HTMLElement
      if (t.closest('.n-checkbox')) return
      emit('select', row, e)
    },
    onDblclick: (e: MouseEvent) => {
      const t = e.target as HTMLElement
      if (t.closest('.n-checkbox, .more-btn')) return
      emit('open', row)
    },
    onContextmenu: (e: MouseEvent) => {
      e.preventDefault()
      e.stopPropagation()
      emit('contextmenu', row, e)
    },
  }
}

const virtual = computed(() => props.entries.length > 200)
const vars = useThemeVars()
</script>

<template>
  <n-data-table
    class="file-table"
    :style="{ '--lnf-primary': vars.primaryColor }"
    :columns="columns"
    :data="entries"
    :row-key="(r: Entry) => r.path"
    :checked-row-keys="selected"
    :row-props="rowProps"
    :loading="loading"
    :bordered="false"
    :single-line="true"
    :virtual-scroll="virtual"
    flex-height
    size="small"
    @update:checked-row-keys="(k) => emit('update:selected', k as string[])"
    @update:sorter="onSorter"
  >
    <template #empty><slot name="empty" /></template>
  </n-data-table>
</template>

<style scoped>
.file-table {
  height: 100%;
}
.file-table :deep(.name-cell) {
  display: flex;
  align-items: center;
  gap: 10px;
  min-width: 0;
}
.file-table :deep(.name-main) {
  min-width: 0;
  flex: 1;
}
.file-table :deep(.name-link) {
  display: block;
  cursor: pointer;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  color: inherit;
  text-decoration: none;
}
.file-table :deep(.name-link:hover) {
  color: var(--lnf-primary);
  text-decoration: underline;
}
.file-table :deep(.name-sub) {
  font-size: 12px;
  opacity: 0.55;
  margin-top: 1px;
}
.file-table :deep(.muted) {
  opacity: 0.7;
  font-variant-numeric: tabular-nums;
}
.file-table :deep(.ro-tag) {
  flex-shrink: 0;
}
.file-table :deep(.row-selected td) {
  background-color: var(--n-td-color-hover) !important;
}
.file-table :deep(td) {
  user-select: none;
}
</style>
