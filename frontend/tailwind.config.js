/** @type {import('tailwindcss').Config} */
export default {
  content: ['./index.html', './src/**/*.{vue,js,ts,jsx,tsx}'],
  darkMode: 'class',
  theme: {
    extend: {
      colors: {
        slate: Object.fromEntries([50, 100, 200, 300, 400, 500, 600, 700, 800, 900, 950].map(shade => [shade, `rgb(var(--color-slate-${shade}) / <alpha-value>)`])),
        amber: { 500: 'rgb(var(--color-accent) / <alpha-value>)' },
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
