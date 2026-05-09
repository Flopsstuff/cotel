import { useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { useSession } from '../api'
import { Card, KpiCard, DataTable, EmptyState, ErrorState, LoadingSkeleton, sessionStatusBadge } from '../components'
import type { SpanDetail } from '../api'
import styles from './SessionDetail.module.css'

function fmtDuration(ms: number): string {
  if (ms >= 60_000) return `${(ms / 60_000).toFixed(1)}m`
  if (ms >= 1_000) return `${(ms / 1_000).toFixed(1)}s`
  return `${ms.toFixed(0)}ms`
}

export default function SessionDetail() {
  const { id } = useParams<{ id: string }>()
  const { data, error, isLoading } = useSession(id ?? '')
  const [expanded, setExpanded] = useState<Set<number>>(new Set())

  function toggleExpanded(i: number) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(i)) next.delete(i)
      else next.add(i)
      return next
    })
  }

  if (isLoading) return <LoadingSkeleton rows={8} height={44} />
  if (error) {
    const is404 = error.message.includes('404')
    return is404 ? (
      <div className={styles.notFound}>
        <p className={styles.notFoundTitle}>Session not found</p>
        <p className={styles.notFoundSub}>Session <code>{id}</code> does not exist.</p>
        <Link to="/sessions" className={styles.backLink}>← Back to Sessions</Link>
      </div>
    ) : (
      <ErrorState message={error.message} />
    )
  }
  if (!data) return null

  const minTime = Math.min(...data.spans.map((s) => new Date(s.start_time).getTime()))
  const maxTime = Math.max(...data.spans.map((s) => new Date(s.start_time).getTime() + s.duration_ms))
  const totalRange = Math.max(maxTime - minTime, 1)
  const errorCount = data.spans.filter((s) => s.status === 'error').length

  return (
    <div>
      <div className={styles.nav}>
        <Link to="/sessions" className={styles.backLink}>← Sessions</Link>
      </div>

      <h1 className={styles.title}>
        <span className={styles.sessionId}>{data.session_id}</span>
      </h1>

      <div className={styles.kpiRow}>
        <KpiCard label="First Seen" value={new Date(data.first_seen).toLocaleString()} />
        <KpiCard label="Last Seen" value={new Date(data.last_seen).toLocaleString()} />
        <KpiCard label="Total Cost" value={`$${data.cost_usd.toFixed(4)}`} />
        <KpiCard label="Input Tokens" value={data.input_tokens.toLocaleString()} />
        <KpiCard label="Output Tokens" value={data.output_tokens.toLocaleString()} />
        <KpiCard label="Errors" value={String(errorCount)} />
      </div>

      {data.spans.length === 0 ? (
        <EmptyState heading="No spans recorded" subtext="This session has no span data." />
      ) : (
        <>
          <Card title={`Waterfall — ${data.spans.length} spans`}>
            <div className={styles.waterfallWrapper}>
              <table className={styles.waterfallTable}>
                <thead>
                  <tr>
                    <th className={styles.wfTh} style={{ width: '28%' }}>Name</th>
                    <th className={styles.wfTh} style={{ width: '42%' }}>Timeline</th>
                    <th className={styles.wfTh} style={{ width: '12%' }}>Duration</th>
                    <th className={styles.wfTh} style={{ width: '10%' }}>Status</th>
                    <th className={styles.wfTh} style={{ width: '8%' }}></th>
                  </tr>
                </thead>
                <tbody>
                  {data.spans.map((span, i) => {
                    const spanStart = new Date(span.start_time).getTime()
                    const offsetPct = ((spanStart - minTime) / totalRange) * 100
                    const widthPct = Math.max((span.duration_ms / totalRange) * 100, 0.5)
                    const isOpen = expanded.has(i)
                    return (
                      <>
                        <tr
                          key={i}
                          className={[styles.wfRow, styles.wfRowClickable].join(' ')}
                          onClick={() => toggleExpanded(i)}
                        >
                          <td className={styles.wfTd}>
                            <span className={styles.spanName} title={span.name}>{span.name}</span>
                            {span.tool_name && (
                              <span className={styles.toolName}>{span.tool_name}</span>
                            )}
                          </td>
                          <td className={styles.wfTd}>
                            <div className={styles.waterfallCell}>
                              <div
                                className={[
                                  styles.waterfallBar,
                                  span.status === 'error' ? styles.barError : styles.barOk,
                                ].join(' ')}
                                style={{ left: `${offsetPct}%`, width: `${widthPct}%` }}
                              />
                            </div>
                          </td>
                          <td className={styles.wfTd}>
                            <span className={styles.duration}>{fmtDuration(span.duration_ms)}</span>
                          </td>
                          <td className={styles.wfTd}>{sessionStatusBadge(span.status)}</td>
                          <td className={styles.wfTd}>
                            <span className={styles.expandIcon}>{isOpen ? '▲' : '▼'}</span>
                          </td>
                        </tr>
                        {isOpen && (
                          <tr key={`${i}-detail`} className={styles.detailRow}>
                            <td colSpan={5} className={styles.detailCell}>
                              <pre className={styles.attrPre}>
                                {(() => {
                                  try { return JSON.stringify(JSON.parse(span.attributes), null, 2) }
                                  catch { return span.attributes }
                                })()}
                              </pre>
                              {span.input_tokens != null && (
                                <div className={styles.tokenRow}>
                                  <span>In: {span.input_tokens?.toLocaleString()}</span>
                                  <span>Out: {span.output_tokens?.toLocaleString()}</span>
                                  {span.model && <span>Model: {span.model}</span>}
                                </div>
                              )}
                            </td>
                          </tr>
                        )}
                      </>
                    )
                  })}
                </tbody>
              </table>
            </div>
          </Card>

          <Card title="Spans">
            <DataTable<SpanDetail>
              columns={[
                {
                  key: 'start_time',
                  label: 'Time',
                  sortable: true,
                  render: (v) => new Date(String(v)).toLocaleTimeString(),
                },
                { key: 'name', label: 'Name', sortable: true },
                { key: 'tool_name', label: 'Tool', sortable: true },
                {
                  key: 'duration_ms',
                  label: 'Duration',
                  sortable: true,
                  render: (v) => fmtDuration(Number(v)),
                },
                {
                  key: 'input_tokens',
                  label: 'In Tokens',
                  sortable: true,
                  render: (v) => v != null ? Number(v).toLocaleString() : '—',
                },
                {
                  key: 'output_tokens',
                  label: 'Out Tokens',
                  sortable: true,
                  render: (v) => v != null ? Number(v).toLocaleString() : '—',
                },
                {
                  key: 'status',
                  label: 'Status',
                  render: (v) => sessionStatusBadge(String(v)),
                },
              ]}
              rows={data.spans}
            />
          </Card>
        </>
      )}
    </div>
  )
}
