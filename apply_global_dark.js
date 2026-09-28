const fs = require('fs');
const path = require('path');

const replacements = [
    [/bg-slate-50(?! dark:bg-slate-)/g, 'bg-slate-50 dark:bg-slate-950'],
    [/bg-white(?! dark:bg-slate-)/g, 'bg-white dark:bg-slate-900'],
    [/border-slate-200(?! dark:border-slate-)/g, 'border-slate-200 dark:border-slate-700'],
    [/border-slate-100(?! dark:border-slate-)/g, 'border-slate-100 dark:border-slate-800'],
    [/border-slate-300(?! dark:border-slate-)/g, 'border-slate-300 dark:border-slate-600'],
    [/text-slate-900(?! dark:text-slate-)/g, 'text-slate-900 dark:text-slate-100'],
    [/text-slate-800(?! dark:text-slate-)/g, 'text-slate-800 dark:text-slate-200'],
    [/text-slate-700(?! dark:text-slate-)/g, 'text-slate-700 dark:text-slate-300'],
    [/text-slate-600(?! dark:text-slate-)/g, 'text-slate-600 dark:text-slate-400'],
    [/text-slate-500(?! dark:text-slate-)/g, 'text-slate-500 dark:text-slate-400'],
    [/text-slate-400(?! dark:text-slate-)/g, 'text-slate-400 dark:text-slate-500'],
    [/bg-slate-100(?! dark:bg-slate-)/g, 'bg-slate-100 dark:bg-slate-800'],
    [/bg-slate-200(?! dark:bg-slate-)/g, 'bg-slate-200 dark:bg-slate-700'],
    [/hover:bg-slate-50(?! dark:hover:bg-slate-)/g, 'hover:bg-slate-50 dark:hover:bg-slate-800/50'],
    [/hover:bg-slate-100(?! dark:hover:bg-slate-)/g, 'hover:bg-slate-100 dark:hover:bg-slate-800'],
    [/hover:bg-gray-100(?! dark:hover:bg-gray-)/g, 'hover:bg-gray-100 dark:hover:bg-gray-800'],
    [/hover:text-slate-900(?! dark:hover:text-slate-)/g, 'hover:text-slate-900 dark:hover:text-slate-100'],
    [/hover:text-slate-800(?! dark:hover:text-slate-)/g, 'hover:text-slate-800 dark:hover:text-slate-200'],
    [/hover:text-slate-700(?! dark:hover:text-slate-)/g, 'hover:text-slate-700 dark:hover:text-slate-300'],
    [/ring-slate-200(?! dark:ring-slate-)/g, 'ring-slate-200 dark:ring-slate-700'],
    [/divide-slate-200(?! dark:divide-slate-)/g, 'divide-slate-200 dark:divide-slate-700'],
    [/divide-slate-100(?! dark:divide-slate-)/g, 'divide-slate-100 dark:divide-slate-800']
];

function processDirectory(dir) {
    const files = fs.readdirSync(dir);
    for (const file of files) {
        const fullPath = path.join(dir, file);
        if (fs.statSync(fullPath).isDirectory()) {
            processDirectory(fullPath);
        } else if (fullPath.endsWith('.vue')) {
            let content = fs.readFileSync(fullPath, 'utf8');
            let modified = false;
            for (const [regex, replace] of replacements) {
                const newContent = content.replace(regex, replace);
                if (newContent !== content) {
                    content = newContent;
                    modified = true;
                }
            }
            if (modified) {
                fs.writeFileSync(fullPath, content, 'utf8');
                console.log(`Updated ${fullPath}`);
            }
        }
    }
}

processDirectory('frontend/src/views');
processDirectory('frontend/src/components');
