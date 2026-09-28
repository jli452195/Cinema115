const fs = require('fs');

const path = 'frontend/src/components/ManagementLayout.vue';
let content = fs.readFileSync(path, 'utf8');

// replace bg-slate-50
content = content.replace(
  'class="flex min-h-screen flex-col bg-slate-50"',
  'class="flex min-h-screen flex-col bg-slate-50 dark:bg-slate-950"'
);

// replace text-slate-900
content = content.replace(
  'text-slate-900',
  'text-slate-900 dark:text-slate-100'
);

content = content.replace(
  'hover:text-slate-700',
  'hover:text-slate-700 dark:hover:text-slate-300'
);
content = content.replace(
  'text-slate-600',
  'text-slate-600 dark:text-slate-300'
);

fs.writeFileSync(path, content, 'utf8');

const appPath = 'frontend/src/App.vue';
let appContent = fs.readFileSync(appPath, 'utf8');
appContent = appContent.replace(
  'bg-slate-50 p-6 text-sm text-slate-600',
  'bg-slate-50 p-6 text-sm text-slate-600 dark:bg-slate-950 dark:text-slate-300'
);
fs.writeFileSync(appPath, appContent, 'utf8');

const homePath = 'frontend/src/views/HomeView.vue';
let homeContent = fs.readFileSync(homePath, 'utf8');
homeContent = homeContent.replace(
  /bg-white/g,
  'bg-white dark:bg-slate-900'
);
homeContent = homeContent.replace(
  /border-slate-200/g,
  'border-slate-200 dark:border-slate-700'
);
homeContent = homeContent.replace(
  /border-slate-300/g,
  'border-slate-300 dark:border-slate-600'
);
homeContent = homeContent.replace(
  /text-slate-900/g,
  'text-slate-900 dark:text-slate-100'
);
homeContent = homeContent.replace(
  /text-slate-500/g,
  'text-slate-500 dark:text-slate-400'
);
homeContent = homeContent.replace(
  /border-slate-100/g,
  'border-slate-100 dark:border-slate-800'
);
homeContent = homeContent.replace(
  /bg-blue-50/g,
  'bg-blue-50 dark:bg-blue-900/30'
);
homeContent = homeContent.replace(
  /bg-emerald-50/g,
  'bg-emerald-50 dark:bg-emerald-900/30'
);
fs.writeFileSync(homePath, homeContent, 'utf8');
