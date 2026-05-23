import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { listProjects } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { ProjectView } from '@/api'
import { useSession } from '@/composables/useSession'

export function useProjectsMap() {
  const session = useSession()
  // Projects are user-bound: every caller with view_own_usage sees their own
  // rows (admin auto-passes can()). The fetcher returns only the caller's
  // projects; cross-account labels (e.g. someone else's request showing on
  // an admin dashboard view) fall back to ID via projectLabel().
  const canView = computed(() => session.can('view_own_usage'))
  const query = useQuery({
    queryKey: queryKeys.projects.all,
    queryFn: listProjects,
    enabled: canView,
  })
  const projects = computed(() => query.data.value ?? [])
  const projectsMap = computed(() => {
    const m = new Map<number, ProjectView>()
    for (const p of projects.value) m.set(p.id, p)
    return m
  })

  function projectLabel(id: number): string {
    const p = projectsMap.value.get(id)
    return p ? p.name : `#${id}`
  }

  return { projects, projectsMap, projectLabel, fetchProjects: query.refetch, query }
}
