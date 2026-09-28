const fs = require('fs');

const path = 'frontend/src/components/MediaRecommendations.vue';
let content = fs.readFileSync(path, 'utf8');

// The scrollbar color is hardcoded in the <style> block
// .recommendation-rail { scrollbar-width: thin; scrollbar-color: #cbd5e1 transparent; }
const badStyle = `.recommendation-rail { scrollbar-width: thin; scrollbar-color: #cbd5e1 transparent; }`;
const goodStyle = `.recommendation-rail { scrollbar-width: thin; scrollbar-color: #cbd5e1 transparent; }
:global(.dark) .recommendation-rail { scrollbar-color: #334155 transparent; }`;

content = content.replace(badStyle, goodStyle);

fs.writeFileSync(path, content, 'utf8');

const detailPath = 'frontend/src/views/MediaDetailView.vue';
let detailContent = fs.readFileSync(detailPath, 'utf8');

// If there are other hardcoded scrollbars, let's fix them globally in style.css instead
const stylePath = 'frontend/src/style.css';
let styleContent = fs.readFileSync(stylePath, 'utf8');

if (!styleContent.includes('::-webkit-scrollbar')) {
    styleContent += `

/* Global Scrollbar Dark Mode Support */
::-webkit-scrollbar {
  height: 8px;
  width: 8px;
}
::-webkit-scrollbar-track {
  background: transparent;
}
::-webkit-scrollbar-thumb {
  background-color: #cbd5e1;
  border-radius: 4px;
}
.dark ::-webkit-scrollbar-thumb {
  background-color: #334155;
}
.dark * {
  scrollbar-color: #334155 transparent;
}
`;
    fs.writeFileSync(stylePath, styleContent, 'utf8');
}
