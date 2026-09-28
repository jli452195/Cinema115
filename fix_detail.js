const fs = require('fs');

const path = 'frontend/src/views/MediaDetailView.vue';
let content = fs.readFileSync(path, 'utf8');

// Fix text-slate-950 which wasn't replaced properly
content = content.replace(/text-slate-950/g, 'text-slate-950 dark:text-slate-50');

// Fix background of details 
content = content.replace(/bg-slate-100(?! dark:bg-slate-)/g, 'bg-slate-100 dark:bg-slate-800');

// Fix buttons background hover colors that weren't converted properly
content = content.replace(/hover:bg-emerald-50(?! dark:hover:bg-emerald-)/g, 'hover:bg-emerald-50 dark:hover:bg-emerald-900/30');
content = content.replace(/hover:bg-blue-50(?! dark:hover:bg-blue-)/g, 'hover:bg-blue-50 dark:hover:bg-blue-900/30');
content = content.replace(/hover:bg-violet-50(?! dark:hover:bg-violet-)/g, 'hover:bg-violet-50 dark:hover:bg-violet-900/30');
content = content.replace(/hover:bg-amber-50(?! dark:hover:bg-amber-)/g, 'hover:bg-amber-50 dark:hover:bg-amber-900/30');

// Fix text dark mode overlapping duplication (like dark:text-slate-400 dark:text-slate-500)
content = content.replace(/(dark:text-slate-(400|500) ){2,}/g, 'dark:text-slate-400 ');
content = content.replace(/dark:text-slate-500 dark:text-slate-400/g, 'dark:text-slate-400');
content = content.replace(/dark:text-slate-400 dark:text-slate-500/g, 'dark:text-slate-400');

fs.writeFileSync(path, content, 'utf8');
