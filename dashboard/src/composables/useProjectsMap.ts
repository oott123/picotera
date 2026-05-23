import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { listProjects } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { ProjectView } from '@/api'
import { useSession } from '@/composables/useSession'

export function useProjectsMap() {
  const session = useSession()
  // admin-only data; non-admin sees empty map and ID fallback labels
  const query = useQuery({
    queryKey: queryKeys.projects.all,
    queryFn: listProjects,
    enabled: session.isAdmin,
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
