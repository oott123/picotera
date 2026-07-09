import type { Component } from 'vue'
import {
  IconActivity,
  IconAlignJustified,
  IconArrowLeft,
  IconBolt,
  IconBraces,
  IconChartPie,
  IconCheck,
  IconChevronDown,
  IconCloudDollar,
  IconCloudDownload,
  IconFilter,
  IconFilterPlus,
  IconCloudUpload,
  IconCpu,
  IconCurrencyDollar,
  IconDatabase,
  IconEdit,
  IconFolder,
  IconGitBranch,
  IconGitMerge,
  IconKey,
  IconCopy,
  IconPalette,
  IconLink,
  IconList,
  IconLoader2,
  IconMask,
  IconPlug,
  IconPlus,
  IconPuzzle,
  IconPuzzleOff,
  IconRefresh,
  IconRoute,
  IconSearch,
  IconSettings,
  IconTrash,
  IconX,
  IconCloudFog,
  IconGeometry,
  IconFlask,
  IconUsers,
  IconShieldCheck,
} from '@tabler/icons-vue'

export type IconName =
  | 'plus'
  | 'arrow-left'
  | 'palette'
  | 'edit'
  | 'trash'
  | 'close'
  | 'close-sm'
  | 'link'
  | 'settings'
  | 'cpu'
  | 'plug'
  | 'branch'
  | 'db'
  | 'list'
  | 'lines'
  | 'braces'
  | 'chart-pie'
  | 'activity'
  | 'refresh'
  | 'route'
  | 'chevron-down'
  | 'search'
  | 'cloud-dollar'
  | 'cloud-download'
  | 'cloud-upload'
  | 'loader'
  | 'check'
  | 'puzzle'
  | 'puzzle-off'
  | 'currency-dollar'
  | 'key'
  | 'copy'
  | 'folder'
  | 'git-merge'
  | 'bolt'
  | 'geometry'
  | 'cloud-fog'
  | 'flask'
  | 'users'
  | 'shield-check'
  | 'filter'
  | 'filter-plus'
  | 'mask'

export const iconComponents: Record<IconName, Component> = {
  plus: IconPlus,
  'arrow-left': IconArrowLeft,
  palette: IconPalette,
  edit: IconEdit,
  trash: IconTrash,
  close: IconX,
  'close-sm': IconX,
  link: IconLink,
  settings: IconSettings,
  cpu: IconCpu,
  plug: IconPlug,
  branch: IconGitBranch,
  db: IconDatabase,
  list: IconList,
  lines: IconAlignJustified,
  braces: IconBraces,
  'chart-pie': IconChartPie,
  activity: IconActivity,
  refresh: IconRefresh,
  route: IconRoute,
  'chevron-down': IconChevronDown,
  search: IconSearch,
  'cloud-dollar': IconCloudDollar,
  'cloud-download': IconCloudDownload,
  'cloud-upload': IconCloudUpload,
  loader: IconLoader2,
  check: IconCheck,
  puzzle: IconPuzzle,
  'puzzle-off': IconPuzzleOff,
  'currency-dollar': IconCurrencyDollar,
  key: IconKey,
  copy: IconCopy,
  folder: IconFolder,
  'git-merge': IconGitMerge,
  bolt: IconBolt,
  'cloud-fog': IconCloudFog,
  geometry: IconGeometry,
  flask: IconFlask,
  users: IconUsers,
  'shield-check': IconShieldCheck,
  mask: IconMask,
  filter: IconFilter,
  'filter-plus': IconFilterPlus,
}
