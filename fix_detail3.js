const fs = require('fs');

const path = 'frontend/src/views/MediaDetailView.vue';
let content = fs.readFileSync(path, 'utf8');

// The background color is hardcoded in the <style scoped> block!
// .detail-page { background: #f8fafc; } (which is slate-50)
// And the gradient mask uses #f8fafc!
const badStyle = `  .detail-page { background: #f8fafc; }
  .detail-backdrop { height: 800px; }
  .detail-backdrop img { opacity: 0.12; }
  .detail-backdrop::after { content: ''; position: absolute; inset: 0; background: linear-gradient(90deg, rgb(248 250 252 / 65%), transparent 70%), linear-gradient(180deg, rgb(248 250 252 / 15%) 25%, #f8fafc 100%); }`;

const goodStyle = `  .detail-page { background: #f8fafc; }
  .dark .detail-page { background: #020617; } /* slate-950 */
  .detail-backdrop { height: 800px; }
  .detail-backdrop img { opacity: 0.12; }
  .dark .detail-backdrop img { opacity: 0.25; } /* Make image slightly more visible in dark mode */
  .detail-backdrop::after { content: ''; position: absolute; inset: 0; background: linear-gradient(90deg, rgb(248 250 252 / 65%), transparent 70%), linear-gradient(180deg, rgb(248 250 252 / 15%) 25%, #f8fafc 100%); }
  .dark .detail-backdrop::after { background: linear-gradient(90deg, rgb(2 6 23 / 75%), transparent 70%), linear-gradient(180deg, rgb(2 6 23 / 15%) 25%, #020617 100%); }`;

content = content.replace(badStyle, goodStyle);

fs.writeFileSync(path, content, 'utf8');
