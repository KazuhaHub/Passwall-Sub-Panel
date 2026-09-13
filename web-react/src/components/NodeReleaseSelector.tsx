import { useEffect, useId, useMemo, useRef, useState } from 'react'
import { Accordion, AccordionDetails, AccordionSummary, Alert, Box, Button, CircularProgress, Link, MenuItem, Stack, TextField, Typography } from '@mui/material'
import ExpandMoreIcon from '@mui/icons-material/ExpandMore'
import { useTranslation } from 'react-i18next'
import { listNodeReleases, type NodeRelease, type NodeReleaseChannel } from '@/api/nodeReleases'
import type { NativeInstallationSelection } from '@/api/servers'

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
  // Keep externally supplied metadata out of href unless it names exactly
  // this project's official tag page, without credentials/query/fragment.
  if (!/^v[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z.-]+)?$/.test(release.version)) return undefined
  const expected = `https://github.com/KazuhaHub/Passwall-Node/releases/tag/${release.version}`
  return release.release_url === expected ? expected : undefined
}

export default function NodeReleaseSelector({ enabled, selection, value, onChange, disabled = false, initialChannel = 'stable', compact = false }: NodeReleaseSelectorProps) {
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
    release.channel === channel && officialReleaseURL(release) && supportsSelection(release, selection),
  ), [releases, channel, selection])
  const selected = options.find(release => release.version === value)
  const selectedURL = selected ? officialReleaseURL(selected) : undefined

  useEffect(() => {
    if (value && !selected) onChangeRef.current('')
  }, [value, selected])

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
    <TextField select fullWidth label={t('admin:servers.native.agent_version')} value={selected ? value : ''}
      disabled={disabled || loading || failed || options.length === 0}
      helperText={compact ? undefined : t('admin:servers.native.release_version_hint')}
      onChange={event => {
        const next = event.target.value
        if (next === '' || options.some(release => release.version === next)) onChangeRef.current(next)
      }}>
      <MenuItem value="">{t('admin:servers.native.release_choose_version')}</MenuItem>
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
      {t(channel === 'stable' ? 'admin:servers.native.release_no_stable' : 'admin:servers.native.release_no_testing')}
    </Alert>}
    {details && (compact ? <Accordion key={value} disableGutters elevation={0} slotProps={{ transition: { unmountOnExit: true } }}>
      <AccordionSummary id={reviewID} aria-controls={`${reviewID}-details`} expandIcon={<ExpandMoreIcon />}>
        <Typography variant="body2">{t('admin:servers.native.release_review')}</Typography>
      </AccordionSummary>
      <AccordionDetails>{details}</AccordionDetails>
    </Accordion> : details)}
  </Stack>
}
