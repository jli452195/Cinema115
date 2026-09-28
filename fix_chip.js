const fs = require('fs');

const path = 'frontend/src/views/DashboardView.vue';
let content = fs.readFileSync(path, 'utf8');

const badChip = /function chip\(active: boolean\) { return active \? '.*' : '.*' }/;
const goodChip = `function chip(active: boolean) { return active ? 'border-amber-400 bg-amber-50 text-amber-600 dark:border-amber-500/50 dark:bg-amber-500/10 dark:text-amber-400' : 'border-transparent text-slate-500 hover:bg-slate-100 hover:text-slate-800 dark:text-slate-400 dark:hover:bg-slate-800/50 dark:hover:text-slate-200' }`;

if (badChip.test(content)) {
    content = content.replace(badChip, goodChip);
    fs.writeFileSync(path, content, 'utf8');
} else {
    console.log("Could not find the chip function using regex");
}
