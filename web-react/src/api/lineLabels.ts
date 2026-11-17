const cleanList = (values?: string[]) => [...new Set((values ?? []).map(value => value.trim()).filter(Boolean))]

/** Line labels mirror the server's normalization (trim; tags de-empty,
 *  de-duplicated, order kept). An absent field stays absent so the server
 *  keeps its saved value instead of clearing it. */
export function lineLabelsInput(line: { region?: string; tags?: string[] }): { region?: string; tags?: string[] } {
  const out: { region?: string; tags?: string[] } = {}
  if (line.region !== undefined) out.region = line.region.trim()
  if (line.tags !== undefined) out.tags = cleanList(line.tags)
  return out
}

/** Splits the node form's comma-joined tag text the same way the node save does. */
export const splitTagsText = (text: string) => cleanList(text ? text.split(',') : [])
