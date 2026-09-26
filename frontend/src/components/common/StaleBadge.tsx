import { Chip } from '@mui/material'
import { History } from 'lucide-react'

interface StaleBadgeProps {
  stale: boolean
  inputVersion: number
  currentVersion: number
}

export function StaleBadge({ stale, inputVersion, currentVersion }: StaleBadgeProps) {
  if (!stale) {
    return <Chip size="small" className="status-badge status-current" label={`INPUT V${inputVersion}`} />
  }
  return (
    <Chip
      size="small"
      className="status-badge status-stale"
      icon={<History size={13} />}
      label={`EXPIRED · RUN INPUT V${inputVersion} / CURRENT V${currentVersion}`}
    />
  )
}
