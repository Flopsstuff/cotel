import { useState } from 'react'
import { Settings as SettingsIcon } from 'lucide-react'
import { useSettings, updateSettings } from '../api'
import { LoadingSkeleton, ErrorState } from '../components'
import styles from './Settings.module.css'

export default function Settings() {
  const { data, error, isLoading, mutate } = useSettings()
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)

  const handleToggle = async (checked: boolean) => {
    setSaving(true)
    setSaveError(null)
    try {
      await updateSettings({ allow_anonymous: checked })
      await mutate()
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Save failed')
    } finally {
      setSaving(false)
    }
  }

  return (
    <div>
      <div className={styles.header}>
        <SettingsIcon size={20} className={styles.headerIcon} />
        <div>
          <h1 className={styles.title}>Settings</h1>
          <p className={styles.subtitle}>Runtime configuration for this cotel instance.</p>
        </div>
      </div>

      {isLoading ? (
        <LoadingSkeleton rows={3} height={32} />
      ) : error ? (
        <ErrorState message={error.message} />
      ) : (
        <div className={styles.section}>
          <div className={styles.sectionTitle}>OTLP Ingest</div>

          <div className={styles.settingRow}>
            <div className={styles.settingInfo}>
              <div className={styles.settingLabel}>Allow anonymous OTLP</div>
              <div className={styles.settingDesc}>
                When enabled, spans without an <code className={styles.code}>Authorization</code> header are accepted and attributed to no user.
                Disable to require every OTLP sender to present a valid user token.
              </div>
            </div>
            <label className={styles.toggle}>
              <input
                type="checkbox"
                className={styles.toggleInput}
                checked={data?.allow_anonymous ?? true}
                disabled={saving}
                onChange={(e) => handleToggle(e.target.checked)}
              />
              <span className={styles.toggleTrack} />
            </label>
          </div>

          {data?.allow_anonymous === false && (
            <div className={styles.warning}>
              Anonymous OTLP is disabled. All ingest requests must include{' '}
              <code className={styles.code}>Authorization: Bearer &lt;token&gt;</code>.{' '}
              Go to <a href="/users" className={styles.link}>Users</a> to manage tokens.
            </div>
          )}

          {saveError && (
            <div className={styles.errorMsg}>{saveError}</div>
          )}
        </div>
      )}
    </div>
  )
}
