<script setup lang="ts">
withDefaults(defineProps<{ name: string; size?: number; stroke?: number }>(), {
  size: 18,
  stroke: 1.8,
})

// 统一线性图标，避免各页面混用 emoji 导致风格不一致
const paths: Record<string, string[]> = {
  plus: ['M12 5v14', 'M5 12h14'],
  folder: [
    'M3.5 7.5a2 2 0 0 1 2-2h4l1.8 2h7.2a2 2 0 0 1 2 2v7a2 2 0 0 1-2 2h-13a2 2 0 0 1-2-2z',
  ],
  settings: [
    'M4 7h9',
    'M17 7h3',
    'M4 12h4',
    'M12 12h8',
    'M4 17h11',
    'M19 17h1',
    'M15 5.2a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6z',
    'M10 10.2a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6z',
    'M17 15.2a1.8 1.8 0 1 0 0 3.6 1.8 1.8 0 0 0 0-3.6z',
  ],
  'chevron-down': ['M6 9l6 6 6-6'],
  'chevron-up': ['M6 15l6-6 6 6'],
  check: ['M20 6L9 17l-5-5'],
  x: ['M18 6L6 18', 'M6 6l12 12'],
  brain: [
    'M9.5 4.2a2.8 2.8 0 0 0-2.8 2.8 2.8 2.8 0 0 0-.8 5.4A3.2 3.2 0 0 0 8.5 18H11V4.2z',
    'M14.5 4.2a2.8 2.8 0 0 1 2.8 2.8 2.8 2.8 0 0 1 .8 5.4A3.2 3.2 0 0 1 15.5 18H13V4.2z',
    'M8 8.5h1.5',
    'M14.5 8.5H16',
    'M8 13h1.5',
    'M14.5 13H16',
  ],
  plan: ['M9 4h6', 'M8 4a2 2 0 0 0-2 2v14h12V6a2 2 0 0 0-2-2', 'M9 11h6', 'M9 15h4'],
  build: [
    'M14.7 6.3a4 4 0 0 0-5.4 5.4L4 17v3h3l5.3-5.3a4 4 0 0 0 5.4-5.4l-2.1 2.1-3-3 2.1-2.1z',
  ],
  clipboard: [
    'M9 4h6v3H9z',
    'M9 5H7a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V7a2 2 0 0 0-2-2h-2',
    'M9 11h6',
    'M9 15h4',
  ],
  save: ['M5 3h11l3 3v15H5z', 'M8 3v6h8V3', 'M8 14h8v7H8z'],
  sparkles: [
    'M12 3l1.7 4.3L18 9l-4.3 1.7L12 15l-1.7-4.3L6 9l4.3-1.7z',
    'M18.5 14.5l.8 2 2 .8-2 .8-.8 2-.8-2-2-.8 2-.8z',
  ],
  refresh: ['M20.5 12a8.5 8.5 0 1 1-2.6-6.1', 'M20.5 4v5.5H15'],
  warning: [
    'M12 9v4',
    'M12 17h.01',
    'M10.3 4.1L2 18.2A2 2 0 0 0 3.7 21h16.6a2 2 0 0 0 1.7-2.8L13.7 4.1a2 2 0 0 0-3.4 0z',
  ],
  'arrow-right': ['M5 12h14', 'M13 6l6 6-6 6'],
  'arrow-left': ['M19 12H5', 'M12 19l-7-7 7-7'],
  'arrow-up': ['M12 19V5', 'M5 12l7-7 7 7'],
  'arrow-down': ['M12 5v14', 'M5 12l7 7 7-7'],
  message: ['M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z'],
  user: ['M20 21a8 8 0 0 0-16 0', 'M12 11a4 4 0 1 0 0-8 4 4 0 0 0 0 8z'],
  file: ['M14 3H6a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V9z', 'M14 3v6h6'],
  search: ['M11 4a7 7 0 1 0 0 14 7 7 0 0 0 0-14z', 'M16 16l4 4'],
  terminal: ['M4 6l5 6-5 6', 'M13 18h7'],
  plug: ['M9 7V3', 'M15 7V3', 'M7 7h10v4a5 5 0 0 1-10 0z', 'M12 16v5'],
  bot: [
    'M12 3v3',
    'M7 6h10a4 4 0 0 1 4 4v6a3 3 0 0 1-3 3H6a3 3 0 0 1-3-3v-6a4 4 0 0 1 4-4z',
    'M9 12h.01',
    'M15 12h.01',
    'M9 16h6',
  ],
  book: ['M4 5a2 2 0 0 1 2-2h12v18H6a2 2 0 0 0-2 2z', 'M8 7h8', 'M8 11h6'],
  trash: ['M3 6h18', 'M8 6V4h8v2', 'M6 6l1 14h10l1-14'],
  download: ['M12 3v12', 'M7 10l5 5 5-5', 'M4 21h16'],
  edit: ['M12 20h9', 'M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z'],
  clock: ['M12 8v5l3 2', 'M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z'],
  lock: ['M7 11V8a5 5 0 0 1 10 0v3', 'M5 11h14v10H5z'],
  'circle-dot': ['M12 3a9 9 0 1 0 0 18 9 9 0 0 0 0-18z', 'M12 9a3 3 0 1 0 0 6 3 3 0 0 0 0-6z'],
}

const d = (name: string) => paths[name] || []
</script>

<template>
  <svg
    class="ic"
    :width="size"
    :height="size"
    viewBox="0 0 24 24"
    fill="none"
    stroke="currentColor"
    :stroke-width="stroke"
    stroke-linecap="round"
    stroke-linejoin="round"
    aria-hidden="true"
  >
    <path v-for="(p, i) in d(name)" :key="i" :d="p" />
  </svg>
</template>

<style scoped>
.ic {
  display: block;
  flex-shrink: 0;
}
</style>
