import { useId } from 'react';

export function BrandLogo({ size = 34 }: { size?: number }) {
  const uid = useId().replace(/:/g, '');
  const bgId = `dsh-bg-${uid}`;
  const sparkId = `dsh-spark-${uid}`;

  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 36 36"
      fill="none"
      aria-hidden="true"
      className="brand-logo-svg"
    >
      <defs>
        <linearGradient id={bgId} x1="3" y1="2" x2="33" y2="34" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#3b82f6" />
          <stop offset="52%" stopColor="#2563eb" />
          <stop offset="100%" stopColor="#0891b2" />
        </linearGradient>
        <linearGradient id={sparkId} x1="18" y1="15" x2="18" y2="27" gradientUnits="userSpaceOnUse">
          <stop offset="0%" stopColor="#ffffff" />
          <stop offset="100%" stopColor="#bae6fd" />
        </linearGradient>
      </defs>
      <rect width="36" height="36" rx="10" fill={`url(#${bgId})`} />
      <rect
        x="0.75"
        y="0.75"
        width="34.5"
        height="34.5"
        rx="9.25"
        stroke="#ffffff"
        strokeOpacity="0.22"
        strokeWidth="1.5"
      />
      <path
        d="M11.5 22.5H9.8C7.7 22.5 6 20.8 6 18.7C6 16.8 7.4 15.2 9.2 14.9C9.8 11.3 12.9 8.5 16.7 8.5C20.2 8.5 23.1 10.8 24.1 14C24.5 13.9 24.9 13.8 25.3 13.8C27.9 13.8 30 15.9 30 18.5C30 20.8 28.3 22.5 26 22.5H24.5"
        stroke="#ffffff"
        strokeWidth="2.2"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
      <path d="M18 14.8L22.2 19L18 23.2L13.8 19L18 14.8Z" fill={`url(#${sparkId})`} />
      <path d="M13 26.8H23" stroke="#bae6fd" strokeWidth="2" strokeLinecap="round" />
      <circle cx="13" cy="26.8" r="1.5" fill="#ffffff" />
      <circle cx="23" cy="26.8" r="1.5" fill="#ffffff" />
    </svg>
  );
}
