const fs = require('fs');

const path = 'frontend/src/components/WorkbenchHeader.vue';
let content = fs.readFileSync(path, 'utf8');

// Import ThemeSwitcher
content = content.replace(
  "import CinemaLogo from './CinemaLogo.vue'",
  "import CinemaLogo from './CinemaLogo.vue'\nimport ThemeSwitcher from './ThemeSwitcher.vue'"
);

// Add ThemeSwitcher component before the account button
content = content.replace(
  '<button ref="accountButton"',
  '<ThemeSwitcher />\n        <button ref="accountButton"'
);

// Add dark classes
content = content.replace(
  'border-b border-slate-200/80 bg-white/95 backdrop-blur-md',
  'border-b border-slate-200/80 bg-white/95 backdrop-blur-md dark:border-slate-800 dark:bg-slate-900/95'
);

content = content.replace(
  'bg-white p-2 shadow-xl shadow-slate-900/10',
  'bg-white p-2 shadow-xl shadow-slate-900/10 dark:bg-slate-800 dark:border-slate-700'
);

content = content.replace(
  /hover:bg-slate-100/g,
  'hover:bg-slate-100 dark:hover:bg-slate-700'
);
content = content.replace(
  /text-slate-600/g,
  'text-slate-600 dark:text-slate-300'
);
content = content.replace(
  /text-slate-500/g,
  'text-slate-500 dark:text-slate-400'
);
content = content.replace(
  /text-slate-400/g,
  'text-slate-400 dark:text-slate-500'
);
content = content.replace(
  /border-slate-200/g,
  'border-slate-200 dark:border-slate-700'
);
content = content.replace(
  /border-slate-100/g,
  'border-slate-100 dark:border-slate-700'
);

// Fix CSS
content = content.replace(
  /color: #0f172a;/g,
  'color: #0f172a; }\n.dark .top-link:hover, .dark .top-link.selected { color: #f8fafc; background: #1e293b; '
);
content = content.replace(
  /background: #f8fafc;/g,
  'background: #f8fafc; '
);
content = content.replace(
  /.icon-button:hover { background: #f1f5f9; color: #0f172a; }/g,
  '.icon-button:hover { background: #f1f5f9; color: #0f172a; }\n.dark .icon-button:hover { background: #1e293b; color: #f8fafc; }'
);

fs.writeFileSync(path, content, 'utf8');
