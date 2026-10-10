<script setup lang="ts">
defineProps<{ collapsed?: boolean }>()
</script>

<template>
  <div class="logo" :class="{ collapsed }">
    <svg class="mark" viewBox="0 0 64 64" aria-hidden="true">
      <rect width="64" height="64" rx="14" fill="#18a058" />
      <path d="M14 22a4 4 0 0 1 4-4h9l4 5h15a4 4 0 0 1 4 4v15a4 4 0 0 1-4 4H18a4 4 0 0 1-4-4z" fill="#fff" />
      <circle cx="24" cy="36" r="3" fill="#18a058" />
      <circle cx="40" cy="36" r="3" fill="#18a058" />
      <path d="M27 36h10" stroke="#18a058" stroke-width="2.5" />
    </svg>
    <div v-if="!collapsed" class="text">
      <div class="name">LocalNetwork</div>
      <div class="sub">Files</div>
    </div>
  </div>
</template>

<style scoped>
.logo {
  display: flex;
  align-items: center;
  gap: 10px;
  height: 56px;
  padding: 0 18px;
  overflow: hidden;
  white-space: nowrap;
}
.logo.collapsed {
  justify-content: center;
  padding: 0;
}
.mark {
  width: 30px;
  height: 30px;
  flex-shrink: 0;
}
.name {
  font-weight: 700;
  font-size: 15px;
  line-height: 1.1;
}
.sub {
  font-size: 12px;
  opacity: 0.6;
  line-height: 1.2;
}
</style>
