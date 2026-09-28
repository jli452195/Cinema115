const fs = require('fs');

const path = 'frontend/src/views/MediaDetailView.vue';
let content = fs.readFileSync(path, 'utf8');

const newStyle = `<style scoped>
.detail-page { background: #f8fafc; }
:global(.dark) .detail-page { background: #020617 !important; }
.detail-backdrop { height: 800px; }
.detail-backdrop img { opacity: 0.12; }
:global(.dark) .detail-backdrop img { opacity: 0.25; }
.detail-backdrop::after { content: ''; position: absolute; inset: 0; background: linear-gradient(90deg, rgb(248 250 252 / 65%), transparent 70%), linear-gradient(180deg, rgb(248 250 252 / 15%) 25%, #f8fafc 100%); }
:global(.dark) .detail-backdrop::after { background: linear-gradient(90deg, rgb(2 6 23 / 75%), transparent 70%), linear-gradient(180deg, rgb(2 6 23 / 15%) 25%, #020617 100%) !important; }
@media (prefers-reduced-motion: reduce) {
  .animate-pulse { animation: none; }
  .transition, .transition-transform { transition: none; }
}
</style>`;

content = content.replace(/<style scoped>[\s\S]*<\/style>/, newStyle);
fs.writeFileSync(path, content, 'utf8');
