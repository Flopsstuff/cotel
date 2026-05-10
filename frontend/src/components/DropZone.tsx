import { useRef, useState } from 'react'
import { Upload } from 'lucide-react'
import styles from './DropZone.module.css'

interface DropZoneProps {
  onFile: (file: File) => void
  accept?: string
}

export function DropZone({ onFile, accept = '.zip' }: DropZoneProps) {
  const [dragOver, setDragOver] = useState(false)
  const inputRef = useRef<HTMLInputElement>(null)

  const handleDrop = (e: React.DragEvent<HTMLDivElement>) => {
    e.preventDefault()
    setDragOver(false)
    const file = e.dataTransfer.files[0]
    if (file) onFile(file)
  }

  const handleChange = (e: React.ChangeEvent<HTMLInputElement>) => {
    const file = e.target.files?.[0]
    if (file) onFile(file)
    e.target.value = ''
  }

  return (
    <div
      className={[styles.zone, dragOver ? styles.dragOver : ''].filter(Boolean).join(' ')}
      onDragOver={(e) => { e.preventDefault(); setDragOver(true) }}
      onDragLeave={() => setDragOver(false)}
      onDrop={handleDrop}
      onClick={() => inputRef.current?.click()}
      role="button"
      tabIndex={0}
      onKeyDown={(e) => { if (e.key === 'Enter' || e.key === ' ') { e.preventDefault(); inputRef.current?.click() } }}
    >
      <input
        ref={inputRef}
        type="file"
        accept={accept}
        className={styles.input}
        onChange={handleChange}
      />
      <Upload size={28} className={styles.icon} />
      <p className={styles.text}>Drop a .zip export here</p>
      <p className={styles.sub}>or click to browse</p>
    </div>
  )
}
