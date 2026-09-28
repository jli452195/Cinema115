const fs = require('fs');

const path = 'frontend/src/views/MediaDetailView.vue';
let content = fs.readFileSync(path, 'utf8');

// The main background is currently `bg-slate-50 dark:bg-slate-950`
// However, there is a backdrop image which might be overriding things or the text colors are still hardcoded for light mode.
content = content.replace(/bg-white/g, 'bg-white dark:bg-slate-900');
content = content.replace(/border-slate-200/g, 'border-slate-200 dark:border-slate-700');
content = content.replace(/border-slate-100/g, 'border-slate-100 dark:border-slate-800');
content = content.replace(/text-slate-900/g, 'text-slate-900 dark:text-slate-100');
content = content.replace(/text-slate-800/g, 'text-slate-800 dark:text-slate-200');
content = content.replace(/text-slate-700/g, 'text-slate-700 dark:text-slate-300');
content = content.replace(/text-slate-600/g, 'text-slate-600 dark:text-slate-300');
content = content.replace(/text-slate-500/g, 'text-slate-500 dark:text-slate-400');
content = content.replace(/text-slate-400/g, 'text-slate-400 dark:text-slate-500');

// Fix any duplicated dark: classes again
content = content.replace(/(dark:[a-z-]+-[0-9]+\/?\d*\s*)\1+/g, '$1');

fs.writeFileSync(path, content, 'utf8');
