import type { OrderRow } from '@/api/nodes'
import type { Node } from '@/api/types'
import type { TreeRow } from './listModel'

/** One table row: the order row plus what the node join adds (absent while the join has not loaded). */
export interface ListRow {
  id: string
  row: OrderRow
  node?: Node
  /** The server the landing leaves through, when it is not its own. */
  exitName?: string
}

/** A row of the by-node view: a landing with its lines beneath it. */
export interface TreeItem {
  id: string
  group: TreeRow
  /** The direct line's row; absent when a filter hid it (the group still names the landing). */
  list?: ListRow
  /** A relay line, for the child rows. */
  child?: boolean
  children?: TreeItem[]
}
