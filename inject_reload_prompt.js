const fs = require('fs');

const path = 'frontend/src/App.vue';
let content = fs.readFileSync(path, 'utf8');

if (!content.includes('ReloadPrompt')) {
    content = content.replace(
        "import LoginView from './views/LoginView.vue'",
        "import LoginView from './views/LoginView.vue'\nimport ReloadPrompt from './components/ReloadPrompt.vue'"
    );

    content = content.replace(
        "</RouterView>",
        "</RouterView>\n  <ReloadPrompt />"
    );

    fs.writeFileSync(path, content, 'utf8');
}
