const fs = require('fs');

function addDark(path, replacements) {
    if (!fs.existsSync(path)) return;
    let content = fs.readFileSync(path, 'utf8');
    for (const [search, replace] of replacements) {
        if (!content.includes(replace)) {
            content = content.split(search).join(replace);
        }
    }
    fs.writeFileSync(path, content, 'utf8');
}

addDark('frontend/src/views/UpdatesView.vue', [
    ['bg-slate-50', 'bg-slate-50 dark:bg-slate-950'],
    ['bg-white', 'bg-white dark:bg-slate-900'],
    ['border-slate-200', 'border-slate-200 dark:border-slate-800'],
    ['border-slate-100', 'border-slate-100 dark:border-slate-800'],
    ['text-slate-900', 'text-slate-900 dark:text-slate-100'],
    ['text-slate-800', 'text-slate-800 dark:text-slate-200'],
    ['text-slate-700', 'text-slate-700 dark:text-slate-300'],
    ['text-slate-600', 'text-slate-600 dark:text-slate-300'],
    ['text-slate-500', 'text-slate-500 dark:text-slate-400'],
    ['text-slate-400', 'text-slate-400 dark:text-slate-500'],
    ['bg-slate-100', 'bg-slate-100 dark:bg-slate-800'],
    ['bg-slate-200', 'bg-slate-200 dark:bg-slate-700'],
    ['hover:bg-slate-50', 'hover:bg-slate-50 dark:hover:bg-slate-800/50'],
    ['hover:bg-slate-100', 'hover:bg-slate-100 dark:hover:bg-slate-800'],
    ['ring-slate-200', 'ring-slate-200 dark:ring-slate-800'],
    ['divide-slate-200', 'divide-slate-200 dark:divide-slate-800'],
    ['divide-slate-100', 'divide-slate-100 dark:divide-slate-800'],
    ['shadow-sm', 'shadow-sm dark:shadow-none']
]);

// 顺便覆盖可能用到的公共组件
addDark('frontend/src/components/DashboardFilterDialog.vue', [
    ['bg-white', 'bg-white dark:bg-slate-900'],
    ['border-slate-200', 'border-slate-200 dark:border-slate-800'],
    ['text-slate-900', 'text-slate-900 dark:text-slate-100'],
    ['text-slate-600', 'text-slate-600 dark:text-slate-300'],
    ['text-slate-500', 'text-slate-500 dark:text-slate-400'],
    ['hover:bg-slate-100', 'hover:bg-slate-100 dark:hover:bg-slate-800']
]);

