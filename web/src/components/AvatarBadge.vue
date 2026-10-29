<script setup lang="ts">
// 通用头像：图片 URL 或 emoji/文字兜底
const props = defineProps<{
  value?: string // /assets/... 图片 或 emoji 或空
  fallback?: string // 空值时显示的文字
  size?: number // px
}>()

const AVATAR_DEFAULT = '/assets/avatar.png'
const isImg = (v?: string) => !!v && (v.startsWith('/assets/') || v.startsWith('http'))
</script>

<template>
  <span
    class="badge"
    :style="{ width: (props.size || 34) + 'px', height: (props.size || 34) + 'px', fontSize: Math.round((props.size || 34) * 0.42) + 'px' }"
  >
    <img v-if="isImg(props.value)" class="img" :src="props.value" alt="" />
    <img v-else-if="!props.value && props.fallback === '小柯'" class="img" :src="AVATAR_DEFAULT" alt="" />
    <span v-else class="emoji">{{ props.value || (props.fallback || '？').slice(0, 1) }}</span>
  </span>
</template>

<style scoped>
.badge {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  border-radius: 50%;
  overflow: hidden;
  flex-shrink: 0;
  background: var(--bg-surface-2);
  border: 1px solid var(--border-strong);
  line-height: 1;
  user-select: none;
}

.img {
  width: 100%;
  height: 100%;
  object-fit: cover;
  object-position: center 26%;
}

.emoji {
  font-size: inherit;
}
</style>
