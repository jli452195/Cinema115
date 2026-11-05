/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        slate: Object.fromEntries([50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950].map(shade => [shade, `rgb(var(--color-slate-${shade}) / <alpha-value>)`])),
        amber: { 500: 'rgb(var(--color-accent) / <alpha-value>)' },
        surface: Object.fromEntries(['page', 'card', 'elevated', 'hover'].map(role => [role, `rgb(var(--surface-${role}) / <alpha-value>)`])),
        content: Object.fromEntries(['primary', 'secondary'].map(role => [role, `rgb(var(--content-${role}) / <alpha-value>)`])),
        line: Object.fromEntries(['default', 'control'].map(role => [role, `rgb(var(--line-${role}) / <alpha-value>)`])),
        action: Object.fromEntries(['primary', 'primary-hover', 'on-primary', 'link'].map(role => [role, `rgb(var(--action-${role}) / <alpha-value>)`])),
        selection: Object.fromEntries(['surface', 'content'].map(role => [role, `rgb(var(--selection-${role}) / <alpha-value>)`])),
        status: Object.fromEntries(['success', 'success-surface', 'warning', 'warning-surface', 'danger', 'danger-surface'].map(role => [role, `rgb(var(--status-${role}) / <alpha-value>)`])),
        focus: 'rgb(var(--focus) / <alpha-value>)',
      },
      borderColor: {
        slate: { 800: 'rgb(var(--color-border) / <alpha-value>)' },
      },
      textColor: {
        slate: { 500: 'rgb(var(--color-text-muted) / <alpha-value>)' },
        amber: { 600: 'rgb(var(--color-accent-text) / <alpha-value>)' },
      },
    },
  },
  plugins: [],
}
