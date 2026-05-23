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
  IconCloudUpload,
  IconCpu,
  IconCurrencyDollar,
  IconDatabase,
  IconEdit,
  IconEye,
  IconEyeOff,
  IconFolder,
  IconGitBranch,
  IconKey,
  IconCopy,
  IconLink,
  IconList,
  IconLoader2,
  IconPlug,
  IconPlus,
  IconPuzzle,
  IconPuzzleOff,
  IconRefresh,
  IconRoute,
  IconSearch,
  IconSettings,
  IconTrash,
  IconUser,
  IconX,
  IconCloudFog,
  IconGeometry,
} from '@tabler/icons-vue'

export type IconName =
  | 'plus'
  | 'arrow-left'
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
  | 'eye'
  | 'eye-off'
  | 'puzzle'
  | 'puzzle-off'
  | 'currency-dollar'
  | 'key'
  | 'copy'
  | 'folder'
  | 'bolt'
  | 'geometry'
  | 'cloud-fog'
  | 'user'

export const iconComponents: Record<IconName, Component> = {
  plus: IconPlus,
  'arrow-left': IconArrowLeft,
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
  eye: IconEye,
  'eye-off': IconEyeOff,
  puzzle: IconPuzzle,
  'puzzle-off': IconPuzzleOff,
  'currency-dollar': IconCurrencyDollar,
  key: IconKey,
  copy: IconCopy,
  folder: IconFolder,
  bolt: IconBolt,
  'cloud-fog': IconCloudFog,
  geometry: IconGeometry,
  user: IconUser,
}
