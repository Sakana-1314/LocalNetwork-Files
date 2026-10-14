<script setup lang="ts">
import { NDatePicker, NRadioButton, NRadioGroup } from 'naive-ui'
import { EXPIRY_OPTIONS, type ExpiryPreset } from '@/utils/share'

const preset = defineModel<ExpiryPreset>('preset', { required: true })
const custom = defineModel<number | null>('custom', { required: true })

const isPast = (ts: number): boolean => ts < Date.now() - 24 * 3600 * 1000
</script>

<template>
  <div class="expiry">
    <n-radio-group v-model:value="preset" size="small">
      <n-radio-button v-for="o in EXPIRY_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</n-radio-button>
    </n-radio-group>
    <n-date-picker
      v-if="preset === 'custom'"
      v-model:value="custom"
      type="datetime"
      clearable
      placeholder="选择过期时间"
      :is-date-disabled="isPast"
      class="picker"
    />
  </div>
</template>

<style scoped>
.expiry {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.picker {
  width: 100%;
}
</style>
