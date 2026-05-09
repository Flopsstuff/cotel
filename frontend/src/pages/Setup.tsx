import { useState, useCallback } from 'react'
import { Check, Copy, Settings } from 'lucide-react'
import styles from './Setup.module.css'

const DOCKER_CMD = `docker run -d \\
  --name cotel \\
  -p 4318:4318 \\
  -p 8080:8080 \\
  -v cotel-data:/data \\
  ghcr.io/flopsstuff/cotel:main`

const SETTINGS_JSON = `{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces"
  }
}`

const AGENT_PROMPT = `Enable Claude Code telemetry so my sessions are tracked locally. \
Edit the file ~/.claude/settings.json and merge in the following JSON — create the file if it doesn't exist:

{
  "env": {
    "CLAUDE_CODE_ENABLE_TELEMETRY": "1",
    "CLAUDE_CODE_ENHANCED_TELEMETRY_BETA": "1",
    "OTEL_TRACES_EXPORTER": "otlp",
    "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT": "http://localhost:4318/v1/traces"
  }
}

After saving, tell me to restart Claude Code so the changes take effect.`

function CopyButton({ text }: { text: string }) {
  const [copied, setCopied] = useState(false)

  const copy = useCallback(async () => {
    await navigator.clipboard.writeText(text)
    setCopied(true)
    setTimeout(() => setCopied(false), 2000)
  }, [text])

  return (
    <button className={styles.copyBtn} onClick={copy} title="Copy to clipboard">
      {copied ? <Check size={14} /> : <Copy size={14} />}
      <span>{copied ? 'Copied!' : 'Copy'}</span>
    </button>
  )
}

interface StepProps {
  number: number
  title: string
  description: string
  children: React.ReactNode
}

function Step({ number, title, description, children }: StepProps) {
  return (
    <div className={styles.step}>
      <div className={styles.stepHeader}>
        <span className={styles.stepNumber}>{number}</span>
        <div>
          <div className={styles.stepTitle}>{title}</div>
          <div className={styles.stepDesc}>{description}</div>
        </div>
      </div>
      {children}
    </div>
  )
}

export default function Setup() {
  return (
    <div>
      <div className={styles.header}>
        <Settings size={20} className={styles.headerIcon} />
        <div>
          <h1 className={styles.title}>Setup</h1>
          <p className={styles.subtitle}>
            Get Claude Code telemetry flowing into cotel in three steps.
          </p>
        </div>
      </div>

      <div className={styles.steps}>
        <Step
          number={1}
          title="Start cotel"
          description="One Docker command — data persists in a named volume."
        >
          <div className={styles.codeBlock}>
            <CopyButton text={DOCKER_CMD} />
            <pre className={styles.pre}>{DOCKER_CMD}</pre>
          </div>
          <p className={styles.hint}>
            Dashboard is available at <code className={styles.code}>http://localhost:8080</code> once the container starts.
          </p>
        </Step>

        <Step
          number={2}
          title="Configure Claude Code"
          description="Add these environment variables to your global Claude Code settings."
        >
          <p className={styles.hint}>
            Open <code className={styles.code}>~/.claude/settings.json</code> and merge in:
          </p>
          <div className={styles.codeBlock}>
            <CopyButton text={SETTINGS_JSON} />
            <pre className={styles.pre}>{SETTINGS_JSON}</pre>
          </div>
        </Step>

        <Step
          number={3}
          title="Verify"
          description="Restart Claude Code, run any session, then check the Overview page."
        >
          <ul className={styles.verifyList}>
            <li>Restart Claude Code after editing <code className={styles.code}>settings.json</code>.</li>
            <li>Start any session (a simple question is enough).</li>
            <li>
              Open <a href="/" className={styles.link}>Overview</a> — the session appears within 30 seconds.
            </li>
          </ul>
        </Step>
      </div>

      <div className={styles.promptSection}>
        <div className={styles.promptHeader}>
          <div className={styles.promptTitle}>Agent prompt</div>
          <div className={styles.promptSubtitle}>
            Paste this into any Claude Code session and Claude will configure telemetry for you automatically.
          </div>
        </div>
        <div className={styles.codeBlock}>
          <CopyButton text={AGENT_PROMPT} />
          <pre className={styles.pre}>{AGENT_PROMPT}</pre>
        </div>
      </div>
    </div>
  )
}
