import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, CircularProgress, Link, MenuItem, Stack, TextField, Typography } from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'
import { listNodeReleases, type NodeRelease, type NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeInstallationSelection } from '@/api/servers'
import { compareReleaseVersion, releaseTag } from '@/utils/productVersion'

export interface NodeReleaseSelectorProps {
  enabled: boolean
  selection: NativeInstallationSelection
  value: string
  onChange: (version: string) => void
  disabled?: boolean
  /** Saved preference supplies the opening channel only; temporary changes never persist here. */
  initialChannel?: NodeReleaseChannel
  /** Installation keeps publication metadata folded; upgrades retain their full review surface. */
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
   * below beta9. It is the same rule the panel's admission check applies, so the
   * list cannot offer something the service would reject for being older.
   */
  newerThan?: string
  /**
   * When set, only these versions are offered.
   *
   * The upgrade dialog passes the releases a verified edge actually reaches.
   * `newerThan` narrows by version, which is a weaker claim: a release can be
   * ahead of the node and still be a path nobody has walked, and offering it
   * invites a request the edge check refuses.
   */
  targets?: readonly string[]
}

function supportsSelection(release: NodeRelease, selection: NativeInstallationSelection): boolean {
  if (!Array.isArray(release.methods) || !release.methods.includes(selection.method)) return false
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
    (!newerThan || compareReleaseVersion(release.version, newerThan) > 0) &&
    (!targets || targets.includes(release.version)),
  ), [releases, channel, selection, newerThan, targets])
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
    if (autoSelectLatest && !disabled && !value && options.length > 0) onChangeRef.current(options[0].version)
  }, [acceptedValue, autoSelectLatest, channelTag, disabled, enabled, failed, loading, options, releases, selection.method, value])

  if (!enabled) return null
  const published = selected && new Date(selected.published_at)
  const publishedLabel = published && !Number.isNaN(published.getTime())
    ? published.toLocaleDateString(i18n.language, { timeZone: 'UTC' })
    : ''

  const details = selected && <Stack spacing={0.5}>
    {publishedLabel && <Typography variant="body2" color="text.secondary">
      {t('admin:servers.native.release_published', { date: publishedLabel })}
    </Typography>}
    {selected.notes && <Typography variant="body2" sx={{ whiteSpace: 'pre-wrap', overflowWrap: 'anywhere' }}>{selected.notes}</Typography>}
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
      {options.map((release, index) => <MenuItem key={release.version} value={release.version} aria-label={release.version}>
        {release.version}{index === 0 ? ` (${t('admin:servers.native.release_recommended')})` : ''}
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
    {details && (compact ? <Accordion key={value} disableGutters elevation={0} slotProps={{ transition: { unmountOnExit: true } }}>
      <AccordionSummary id={reviewID} aria-controls={`${reviewID}-details`} expandIcon={<ExpandMoreIcon />}>
        <Typography variant="body2">{t('admin:servers.native.release_review')}</Typography>
      </AccordionSummary>
      <AccordionDetails>{details}</AccordionDetails>
    </Accordion> : details)}
  </Stack>
}
