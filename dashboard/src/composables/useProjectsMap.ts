import { computed } from 'vue'
import { useQuery } from '@tanstack/vue-query'
import { listProjectLabels } from '@/api/client'
import { queryKeys } from '@/api/queryKeys'
import type { ProjectLabel } from '@/api'

export function useProjectsMap() {
  const query = useQuery({
    queryKey: queryKeys.labels.projects,
    queryFn: listProjectLabels,
  })
  const projects = computed(() => query.data.value ?? [])
  const projectsMap = computed(() => {
    const m = new Map<number, ProjectLabel>()
    for (const p of projects.value) m.set(p.id, p)
    return m
  })

  function projectLabel(id: number): string {
    const p = projectsMap.value.get(id)
    return p ? p.name : `#${id}`
  }

  return { projects, projectsMap, projectLabel, fetchProjects: query.refetch, query }
}
