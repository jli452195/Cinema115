const fs = require('fs');

const path = 'frontend/src/main.ts';
let content = fs.readFileSync(path, 'utf8');

if (!content.includes('useTheme')) {
    content = content.replace(
        "import { router } from './router'",
        "import { router } from './router'\nimport { useTheme } from './composables/useTheme'\n\nuseTheme()\n"
    );
    fs.writeFileSync(path, content, 'utf8');
}
