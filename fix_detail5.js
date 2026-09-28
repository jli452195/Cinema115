const fs = require('fs');

const path = 'frontend/src/views/MediaDetailView.vue';
let content = fs.readFileSync(path, 'utf8');

const newStyle = `<style scoped>
.detail-page { background: #f8fafc; }
html.dark .detail-page { background: #0f172a !important; }
.detail-backdrop { height: 800px; }
.detail-backdrop img { opacity: 0.12; }
html.dark .detail-backdrop img { opacity: 0.25; }
.detail-backdrop::after { content: ''; position: absolute; inset: 0; background: linear-gradient(90deg, rgb(248 250 252 / 65%), transparent 70%), linear-gradient(180deg, rgb(248 250 252 / 15%) 25%, #f8fafc 100%); }
html.dark .detail-backdrop::after { background: linear-gradient(90deg, rgb(15 23 42 / 75%), transparent 70%), linear-gradient(180deg, rgb(15 23 42 / 15%) 25%, #0f172a 100%) !important; }
@media (prefers-reduced-motion: reduce) {
  .animate-pulse { animation: none; }
  .transition, .transition-transform { transition: none; }
}
</style>`;

// Completely strip scoped out to ensure NO vue-loader restrictions block the dark styles
const globalStyle = `<style>
.dark .detail-page { background: #0f172a !important; }
.dark .detail-backdrop img { opacity: 0.25 !important; }
.dark .detail-backdrop::after { background: linear-gradient(90deg, rgb(15 23 42 / 75%), transparent 70%), linear-gradient(180deg, rgb(15 23 42 / 15%) 25%, #0f172a 100%) !important; }
</style>
<style scoped>
.detail-page { background: #f8fafc; }
.detail-backdrop { height: 800px; }
.detail-backdrop img { opacity: 0.12; }
.detail-backdrop::after { content: ''; position: absolute; inset: 0; background: linear-gradient(90deg, rgb(248 250 252 / 65%), transparent 70%), linear-gradient(180deg, rgb(248 250 252 / 15%) 25%, #f8fafc 100%); }
@media (prefers-reduced-motion: reduce) {
  .animate-pulse { animation: none; }
  .transition, .transition-transform { transition: none; }
}
</style>`;

content = content.replace(/<style scoped>[\s\S]*<\/style>/, globalStyle);
fs.writeFileSync(path, content, 'utf8');
