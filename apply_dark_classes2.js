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

addDark('frontend/src/views/DashboardView.vue', [
    ['bg-slate-50', 'bg-slate-50 dark:bg-slate-950'],
    ['bg-white', 'bg-white dark:bg-slate-900'],
    ['border-slate-200', 'border-slate-200 dark:border-slate-800'],
    ['text-slate-900', 'text-slate-900 dark:text-slate-100'],
    ['text-slate-600', 'text-slate-600 dark:text-slate-300'],
    ['text-slate-500', 'text-slate-500 dark:text-slate-400'],
    ['text-slate-400', 'text-slate-400 dark:text-slate-500'],
    ['hover:text-slate-900', 'hover:text-slate-900 dark:hover:text-slate-100'],
    ['hover:text-slate-700', 'hover:text-slate-700 dark:hover:text-slate-300'],
    ['hover:bg-slate-100', 'hover:bg-slate-100 dark:hover:bg-slate-800'],
    ['text-amber-500', 'text-amber-500 dark:text-amber-400'],
    ['border-amber-200', 'border-amber-200 dark:border-amber-700/50'],
    ['bg-amber-50', 'bg-amber-50 dark:bg-amber-900/30'],
    ['text-amber-700', 'text-amber-700 dark:text-amber-400'],
    ['border-slate-300', 'border-slate-300 dark:border-slate-600']
]);

addDark('frontend/src/components/MediaCard.vue', [
    ['bg-white', 'bg-white dark:bg-slate-900'],
    ['text-slate-900', 'text-slate-900 dark:text-slate-100'],
    ['text-slate-500', 'text-slate-500 dark:text-slate-400'],
    ['text-slate-400', 'text-slate-400 dark:text-slate-500'],
    ['bg-slate-100', 'bg-slate-100 dark:bg-slate-800'],
    ['border-slate-200', 'border-slate-200 dark:border-slate-800/50'],
    ['group-hover:border-amber-400', 'group-hover:border-amber-400 dark:group-hover:border-amber-500']
]);

addDark('frontend/src/views/MediaDetailView.vue', [
    ['bg-slate-50', 'bg-slate-50 dark:bg-slate-950'],
    ['bg-white', 'bg-white dark:bg-slate-900'],
    ['text-slate-900', 'text-slate-900 dark:text-slate-100'],
    ['text-slate-800', 'text-slate-800 dark:text-slate-200'],
    ['text-slate-700', 'text-slate-700 dark:text-slate-300'],
    ['text-slate-600', 'text-slate-600 dark:text-slate-300'],
    ['text-slate-500', 'text-slate-500 dark:text-slate-400'],
    ['text-slate-400', 'text-slate-400 dark:text-slate-500'],
    ['border-slate-200', 'border-slate-200 dark:border-slate-800'],
    ['border-slate-100', 'border-slate-100 dark:border-slate-800'],
    ['border-slate-300', 'border-slate-300 dark:border-slate-600'],
    ['bg-slate-100', 'bg-slate-100 dark:bg-slate-800'],
    ['bg-slate-200', 'bg-slate-200 dark:bg-slate-700'],
    ['hover:bg-slate-100', 'hover:bg-slate-100 dark:hover:bg-slate-800'],
    ['hover:text-slate-900', 'hover:text-slate-900 dark:hover:text-slate-100'],
    ['hover:text-slate-800', 'hover:text-slate-800 dark:hover:text-slate-200']
]);

addDark('frontend/src/views/SearchView.vue', [
    ['bg-slate-50', 'bg-slate-50 dark:bg-slate-950'],
    ['bg-white', 'bg-white dark:bg-slate-900'],
    ['text-slate-900', 'text-slate-900 dark:text-slate-100'],
    ['text-slate-500', 'text-slate-500 dark:text-slate-400'],
    ['text-slate-400', 'text-slate-400 dark:text-slate-500'],
    ['border-slate-200', 'border-slate-200 dark:border-slate-800'],
    ['border-slate-300', 'border-slate-300 dark:border-slate-600'],
    ['hover:bg-slate-50', 'hover:bg-slate-50 dark:hover:bg-slate-800/50'],
    ['focus:bg-slate-50', 'focus:bg-slate-50 dark:focus:bg-slate-800/50']
]);
