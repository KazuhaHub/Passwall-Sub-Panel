import { lazy, Suspense, useEffect, useId, useMemo, useRef, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, CircularProgress, Link, MenuItem, Stack, TextField, Typography } from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'
import { listNodeReleases, type NodeRelease, type NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeInstallationSelection } from '@/api/servers'
import { compareReleaseVersion, releaseTag } from '@/utils/productVersion'

// Loaded when the notes are first expanded: the Markdown renderer is only ever
// needed behind the fold, so the dialogs do not carry it until someone asks.
const ReleaseNotes = lazy(() => import('./ReleaseNotes'))

export interface NodeReleaseSelectorProps {
  enabled: boolean
  selection: NativeInstallationSelection
  value: string
  onChange: (version: string) => void
  disabled?: boolean
  /** Saved preference supplies the opening channel only; temporary changes never persist here. */
  initialChannel?: NodeReleaseChannel
  /**
   * The installation layout: channel and version side by side, no helper text.
   * Publication details are folded on EVERY surface, upgrades included — a
   * release body under the version field made the upgrade dialog a page long,
   * and the operator opens it when they want to read it.
   */
  compact?: boolean
  /** Upgrade flows can opt into selecting the newest reviewed release automatically. */
  autoSelectLatest?: boolean
  /**
   * What the caller is selecting for. The empty-state message differs because
   * the reason does: an INSTALL list is empty when the channel has nothing for
   * this platform, and an UPGRADE list is empty when nothing in the channel
   * applies to the node in front of you — which is not the same sentence and,
   * said wrong, sends the operator looking for a release that is not missing.
   */
  context?: 'install' | 'upgrade'
  /**
   * When set, only releases STRICTLY NEWER than this version are offered.
   *
   * The upgrade dialog sets it to the node's own version. A list that includes
   * the version you are already on — and older ones — invites a request the
   * service refuses, and offering a downgrade as though it were a target is how
   * an operator learns to distrust the list instead of the request.
   *
   * The comparison is the project's release order, NOT SemVer's: these are the
   * dotless prerelease tags this project publishes, where SemVer ranks beta11
   * below beta9.
   *
   * IT ANNOTATES; IT DOES NOT FILTER. The claim that this is "the same rule the
   * panel's admission check applies" was false: the panel has no ordering rule at
   * all, and deliberately so — nodeagentupgrade.validateRequest records that an
   * operator choosing an older release is making an explicit choice, guarded by
   * the signed manifest, the checksum, the state-schema equality check and the
   * binary's own version self-report. Hiding those releases made the browser the
   * only place a rule existed, and a rule that exists in one place is a rule two
   * answers can disagree about.
   */
  newerThan?: string
  /**
   * When set, only these versions are offered.
   *
   * The upgrade dialog passes the releases the panel says are installable. It no
   * longer means "a verified edge reaches this": that model was deleted, and the
   * server now answers with every published release other than the node's own.
   */
  targets?: readonly string[]
}

function supportsSelection(release: NodeRelease, selection: NativeInstallationSelection): boolean {
  // THE DOCKER METHOD CONSUMES AN IMAGE, NOT A RELEASE ASSET. Its version may be the
  // floating channel tag and an exact pin is only an image tag, so nothing here has
  // to exist among a release's assets — while the methods a release ADVERTISES are
  // exactly its assets, as the panel's own catalog reader computes them. Requiring
  // an entry for docker therefore emptied this list for every Docker installation,
  // including the reinstall an operator runs to repair one, and the dialog reported
  // an empty channel instead of a method that has no catalog to read.
  if (selection.method !== 'docker' && (!Array.isArray(release.methods) || !release.methods.includes(selection.method))) return false
  if (!Array.isArray(release.platforms)) return false
  if (selection.method === 'manual') {
    return release.platforms.some(platform => platform.os === selection.os && platform.arch === selection.arch)
  }
  // These two recipes detect the host architecture; both Linux assets must
  // exist rather than silently producing a command for an unsupported host.
  return ['amd64', 'arm64'].every(arch => release.platforms.some(platform => platform.os === 'linux' && platform.arch === arch))
}

function officialReleaseURL(release: NodeRelease): string | undefined {
  // Keep externally supplied metadata out of href unless it names exactly this
  // project's official tag page, without credentials/query/fragment.
  //
  // THE PAGE IS ADDRESSED BY THE TAG, AND A TAG IS NOT A VERSION. A product
  // release lives at `tag/release/4.0.0` while its version is `4.0.0`, so a
  // guard that required a v-prefixed version AND rebuilt the URL from the
  // version failed every product release. That is not a broken link: this is
  // called as a FILTER, so the release never appeared in the list, and an
  // operator with nothing to choose from concludes there is nothing to install.
  //
  // releaseTag is what the PANEL states, falling back to the shared rule for a
  // panel older than the field — so the refusals that used to be the regex here,
  // junk and anything readable as a path, are made by one rule either way.
  const tag = releaseTag(release.version, release.release_tag)
  if (!tag) return undefined
  const expected = `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${tag}`
  return release.release_url === expected ? expected : undefined
}

export default function NodeReleaseSelector({ enabled, selection, value, onChange, disabled = false, initialChannel = 'stable', compact = false, autoSelectLatest = false, context = 'install', newerThan, targets }: NodeReleaseSelectorProps) {
  const { t, i18n } = useTranslation(['admin', 'common'])
  const reviewID = useId()
  const [channel, setChannel] = useState<NodeReleaseChannel>(initialChannel)
  const [releases, setReleases] = useState<NodeRelease[] | null>(null)
  const [loading, setLoading] = useState(false)
  const [failed, setFailed] = useState(false)
  const [attempt, setAttempt] = useState(0)
  const onChangeRef = useRef(onChange)
  const valueRef = useRef(value)
  const selectionKey = `${selection.method}:${selection.os}:${selection.arch}`
  const previousSelection = useRef(selectionKey)

  useEffect(() => {
    onChangeRef.current = onChange
    valueRef.current = value
  }, [onChange, value])

  useEffect(() => {
    setChannel(initialChannel)
    if (valueRef.current) onChangeRef.current('')
  }, [enabled, initialChannel])

  useEffect(() => {
    if (previousSelection.current !== selectionKey) {
      previousSelection.current = selectionKey
      onChangeRef.current('')
    }
  }, [selectionKey])

  useEffect(() => {
    const controller = new AbortController()
    setReleases(null)
    setFailed(false)
    if (valueRef.current) onChangeRef.current('')
    if (!enabled) {
      setLoading(false)
      return () => controller.abort()
    }
    setLoading(true)
    void listNodeReleases(controller.signal).then(result => {
      if (controller.signal.aborted) return
      if (!Array.isArray(result.releases)) throw new Error('Invalid release catalog')
      setReleases(result.releases)
    }).catch(() => {
      if (!controller.signal.aborted) setFailed(true)
    }).finally(() => {
      if (!controller.signal.aborted) setLoading(false)
    })
    return () => controller.abort()
  }, [enabled, attempt])

  const options = useMemo(() => (releases ?? []).filter(release =>
    release.channel === channel && officialReleaseURL(release) && supportsSelection(release, selection) &&
    (!targets || targets.includes(release.version)),
  ), [releases, channel, selection, targets])
  // OLDER RELEASES ARE SHOWN AND MARKED, NOT HIDDEN. The server permits them; a
  // list that silently omits what the service would accept is a second opinion,
  // and it was an opinion that switched itself off — the dialog only supplied
  // `newerThan` when the node's reported version happened to parse, so the nodes
  // with the strangest versions got no guidance at all.
  const isOlder = (version: string) => !!newerThan && compareReleaseVersion(version, newerThan) < 0
  // STRICTLY AHEAD, NOT MERELY NOT-OLDER. The two differ on the node's OWN
  // version, which compares equal: the server already omits it from `targets`,
  // but `targets` is undefined whenever that fetch failed, and then the whole
  // catalog is listed. Recommending the version the node is already running — and
  // auto-selecting it — leaves Confirm disabled with no explanation, because the
  // write path refuses the exact no-op.
  //
  // For a version the comparator cannot order, this is false for everything and
  // nothing is auto-selected. That is the honest outcome: with no ordering there
  // is no "latest" to recommend, and guessing is what the label would be doing.
  const isAhead = (version: string) => !newerThan || compareReleaseVersion(version, newerThan) > 0
  // THE FIRST ONE, WHICH RELIES ON THE CATALOG BEING NEWEST-FIRST — it reads
  // GitHub's release list, which is ordered by publication. That dependency
  // predates this: the recommendation used to be options[0] outright. It is named
  // here because it is now load-bearing in a second place.
  const recommended = useMemo(() => options.find(release => isAhead(release.version)), [options, newerThan])
  const selected = options.find(release => release.version === value)
  const selectedURL = selected ? officialReleaseURL(selected) : undefined
  const channelTag = channel === 'stable' ? 'latest' : 'beta'
  const followsDockerChannel = selection.method === 'docker' && value === channelTag && options.length > 0
  const acceptedValue = !!selected || followsDockerChannel

  useEffect(() => {
    if (!enabled || loading || failed || releases === null) return
    if (selection.method === 'docker' && options.length > 0 && !value) {
      onChangeRef.current(channelTag)
      return
    }
    if (value && !acceptedValue) onChangeRef.current('')
    if (autoSelectLatest && !disabled && !value && recommended) onChangeRef.current(recommended.version)
  }, [acceptedValue, autoSelectLatest, channelTag, disabled, enabled, failed, loading, options, recommended, releases, selection.method, value])

  if (!enabled) return null
  const published = selected && new Date(selected.published_at)
  const publishedLabel = published && !Number.isNaN(published.getTime())
    ? published.toLocaleDateString(i18n.language, { timeZone: 'UTC' })
    : ''

  const details = selected && <Stack spacing={0.5}>
    {publishedLabel && <Typography variant="body2" color="text.secondary">
      {t('admin:servers.native.release_published', { date: publishedLabel })}
    </Typography>}
    {selected.notes && <Suspense fallback={<CircularProgress size={16} />}>
      <ReleaseNotes>{selected.notes}</ReleaseNotes>
    </Suspense>}
    {selectedURL && <Link href={selectedURL} target="_blank" rel="noopener noreferrer" variant="body2">
      {t('admin:servers.native.release_details')}
    </Link>}
  </Stack>

  return <Stack spacing={1.5}>
    <Stack direction={compact ? { xs: 'column', sm: 'row' } : 'column'} spacing={1.5}>
    <TextField select fullWidth label={t('admin:servers.native.release_channel')} value={channel} disabled={disabled}
      onChange={event => {
        const next = event.target.value as NodeReleaseChannel
        if (next !== channel) {
          onChangeRef.current('')
          setChannel(next)
        }
      }}>
      <MenuItem value="stable">{t('admin:servers.native.release_stable')}</MenuItem>
      <MenuItem value="testing">{t('admin:servers.native.release_testing')}</MenuItem>
    </TextField>
    <TextField select fullWidth label={t('admin:servers.native.agent_version')} value={acceptedValue ? value : ''}
      disabled={disabled || loading || failed || options.length === 0}
      helperText={compact ? undefined : t(selection.method === 'docker'
        ? 'admin:servers.native.release_version_hint_docker'
        : 'admin:servers.native.release_version_hint')}
      onChange={event => {
        const next = event.target.value
        if (next === '' || (selection.method === 'docker' && next === channelTag) || options.some(release => release.version === next)) onChangeRef.current(next)
      }}>
      <MenuItem value="">{t('admin:servers.native.release_choose_version')}</MenuItem>
      {selection.method === 'docker' && options.length > 0 && <MenuItem value={channelTag} aria-label={channelTag}>
        {t(channel === 'stable' ? 'admin:servers.native.release_follow_stable' : 'admin:servers.native.release_follow_testing')}
        {` (${t('admin:servers.native.release_recommended')})`}
      </MenuItem>}
      {options.map(release => <MenuItem key={release.version} value={release.version} aria-label={release.version}>
        {release.version}
        {isOlder(release.version) ? ` (${t('admin:servers.native.release_older_than_current')})` : ''}
        {release.version === recommended?.version ? ` (${t('admin:servers.native.release_recommended')})` : ''}
      </MenuItem>)}
    </TextField>
    </Stack>
    {loading && <Box role="status" sx={{ display: 'flex', alignItems: 'center', gap: 1 }}>
      <CircularProgress size={18} />
      <Typography variant="body2">{t('admin:servers.native.release_loading')}</Typography>
    </Box>}
    {failed && <Alert severity="error" action={<Button color="inherit" size="small" disabled={disabled}
      onClick={() => setAttempt(current => current + 1)}>{t('admin:servers.native.release_retry')}</Button>}>
      {t('admin:servers.native.release_failed')}
    </Alert>}
    {!loading && !failed && releases !== null && options.length === 0 && <Alert severity="info">
      {context === 'upgrade'
        ? t('admin:servers.native.release_no_target_for_node')
        : t(channel === 'stable' ? 'admin:servers.native.release_no_stable' : 'admin:servers.native.release_no_testing')}
    </Alert>}
    {details && <Accordion key={value} disableGutters elevation={0} slotProps={{ transition: { unmountOnExit: true } }}>
      <AccordionSummary id={reviewID} aria-controls={`${reviewID}-details`} expandIcon={<ExpandMoreIcon />}>
        <Typography variant="body2">{t('admin:servers.native.release_review')}</Typography>
      </AccordionSummary>
      <AccordionDetails>{details}</AccordionDetails>
    </Accordion>}
  </Stack>
}
