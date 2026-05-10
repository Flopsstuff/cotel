interface LogoMarkProps {
  size?: number
}

export function LogoMark({ size = 24 }: LogoMarkProps) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      xmlns="http://www.w3.org/2000/svg"
      aria-label="cotel"
    >
      <rect width="32" height="32" rx="7" fill="#2563eb" />
      <path d="M 7,20 A 5,5 0 0 1 12,25" stroke="white" strokeWidth="2.2" fill="none" strokeLinecap="round" />
      <path d="M 7,16 A 9,9 0 0 1 16,25" stroke="white" strokeWidth="2.2" fill="none" strokeLinecap="round" />
      <path d="M 7,11 A 14,14 0 0 1 21,25" stroke="white" strokeWidth="2.2" fill="none" strokeLinecap="round" />
      <circle cx="7" cy="25" r="2.4" fill="white" />
    </svg>
  )
}
